package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"regix/internal/registry"
)

// maxPlatforms bounds how many members of a multi-platform image are inspected.
const maxPlatforms = 32

// The parts of the OCI and Docker manifest formats that Regix reads.
type descriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations"`
	Platform    *struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
		Variant      string `json:"variant"`
	} `json:"platform"`
}

type manifestDoc struct {
	MediaType    string            `json:"mediaType"`
	ArtifactType string            `json:"artifactType"`
	Config       *descriptor       `json:"config"`
	Layers       []descriptor      `json:"layers"`
	Manifests    []descriptor      `json:"manifests"`
	Annotations  map[string]string `json:"annotations"`
}

type configDoc struct {
	Created      *time.Time `json:"created"`
	Author       string     `json:"author"`
	OS           string     `json:"os"`
	Architecture string     `json:"architecture"`
	Variant      string     `json:"variant"`
	Config       struct {
		User         string              `json:"User"`
		WorkingDir   string              `json:"WorkingDir"`
		Entrypoint   []string            `json:"Entrypoint"`
		Cmd          []string            `json:"Cmd"`
		Env          []string            `json:"Env"`
		ExposedPorts map[string]struct{} `json:"ExposedPorts"`
		Volumes      map[string]struct{} `json:"Volumes"`
		Labels       map[string]string   `json:"Labels"`
		StopSignal   string              `json:"StopSignal"`
	} `json:"config"`
	History []struct {
		Created    *time.Time `json:"created"`
		CreatedBy  string     `json:"created_by"`
		Comment    string     `json:"comment"`
		EmptyLayer bool       `json:"empty_layer"`
	} `json:"history"`
}

// inspect downloads a manifest and what it refers to, and describes it. Tags is left empty.
func (s *Service) inspect(ctx context.Context, id Identity, repo, reference string) (*Image, error) {
	manifest, err := s.registry.Manifest(ctx, id.Credentials, repo, reference)
	if err != nil {
		return nil, err
	}
	var doc manifestDoc
	if err := json.Unmarshal(manifest.Raw, &doc); err != nil {
		return nil, fmt.Errorf("manifest %s is not valid JSON: %w", manifest.Digest, err)
	}
	mediaType := manifest.MediaType
	if mediaType == "" {
		mediaType = doc.MediaType
	}
	image := &Image{
		Repository:  repo,
		Reference:   reference,
		Digest:      manifest.Digest,
		MediaType:   mediaType,
		Annotations: doc.Annotations,
		Manifest:    manifest.Raw,
	}

	if len(doc.Manifests) > 0 || mediaType == registry.MediaTypeOCIIndex || mediaType == registry.MediaTypeDockerList {
		image.Kind = KindIndex
		image.Platforms = s.inspectIndex(ctx, id, repo, doc.Manifests)
	} else {
		image.Platforms = []Platform{s.inspectPlatform(ctx, id, repo, manifest.Digest, mediaType, &doc, true)}
		if hasImageConfig(&doc) {
			image.Kind = KindImage
		} else {
			image.Kind = KindArtifact
			image.ArtifactType = doc.ArtifactType
			if image.ArtifactType == "" && doc.Config != nil {
				image.ArtifactType = doc.Config.MediaType
			}
		}
	}

	seen := map[string]bool{}
	for _, platform := range image.Platforms {
		for _, b := range platform.blobs {
			if !seen[b.digest] {
				seen[b.digest] = true
				image.Size += b.size
			}
		}
		if image.Created == nil && !platform.Attestation {
			image.Created = platform.Created
		}
	}
	return image, nil
}

// inspectIndex describes the members of a multi-platform image, in the order of the index.
func (s *Service) inspectIndex(ctx context.Context, id Identity, repo string, members []descriptor) []Platform {
	if len(members) > maxPlatforms {
		members = members[:maxPlatforms]
	}
	platforms := make([]Platform, len(members))
	var wait sync.WaitGroup
	for i, member := range members {
		wait.Add(1)
		go func() {
			defer wait.Done()
			name := platformName(member)
			attestation := member.Annotations["vnd.docker.reference.type"] == "attestation-manifest" ||
				strings.HasPrefix(name, "unknown")
			failed := func(err error) {
				platforms[i] = Platform{Platform: name, Digest: member.Digest, MediaType: member.MediaType,
					Attestation: attestation, Error: err.Error(), Layers: []Layer{}}
			}
			if !registry.ValidDigest(member.Digest) {
				failed(fmt.Errorf("invalid digest in index"))
				return
			}
			manifest, err := s.registry.Manifest(ctx, id.Credentials, repo, member.Digest)
			if err != nil {
				failed(err)
				return
			}
			var doc manifestDoc
			if err := json.Unmarshal(manifest.Raw, &doc); err != nil {
				failed(err)
				return
			}
			platform := s.inspectPlatform(ctx, id, repo, member.Digest, manifest.MediaType, &doc, !attestation)
			platform.Manifest = manifest.Raw
			platform.Attestation = attestation
			if platform.Platform == "" || attestation {
				platform.Platform = name
			}
			platforms[i] = platform
		}()
	}
	wait.Wait()
	return platforms
}

// inspectPlatform describes a single image manifest. With readConfig it downloads the config
// blob of a container image for its build history and runtime settings.
func (s *Service) inspectPlatform(ctx context.Context, id Identity, repo, digest, mediaType string, doc *manifestDoc, readConfig bool) Platform {
	platform := Platform{Digest: digest, MediaType: mediaType, Layers: []Layer{}}
	for _, layer := range doc.Layers {
		platform.blobs = append(platform.blobs, blob{layer.Digest, layer.Size})
		platform.Size += layer.Size
	}
	if doc.Config == nil {
		platform.Layers = plainLayers(doc.Layers)
		return platform
	}
	platform.blobs = append(platform.blobs, blob{doc.Config.Digest, doc.Config.Size})
	platform.Size += doc.Config.Size

	if !readConfig || !hasImageConfig(doc) || !registry.ValidDigest(doc.Config.Digest) || doc.Config.Size > registry.MaxConfigBytes {
		platform.Layers = plainLayers(doc.Layers)
		return platform
	}
	raw, err := s.registry.Blob(ctx, id.Credentials, repo, doc.Config.Digest, registry.MaxConfigBytes)
	var config configDoc
	if err == nil {
		err = json.Unmarshal(raw, &config)
	}
	if err != nil {
		platform.Error = "image config could not be read: " + err.Error()
		platform.Layers = plainLayers(doc.Layers)
		return platform
	}

	platform.Platform = joinPlatform(config.OS, config.Architecture, config.Variant)
	platform.Created = config.Created
	platform.Author = config.Author
	platform.Config = &RuntimeConfig{
		User:         config.Config.User,
		WorkingDir:   config.Config.WorkingDir,
		Entrypoint:   config.Config.Entrypoint,
		Cmd:          config.Config.Cmd,
		Env:          config.Config.Env,
		ExposedPorts: sortedKeys(config.Config.ExposedPorts),
		Volumes:      sortedKeys(config.Config.Volumes),
		Labels:       config.Config.Labels,
		StopSignal:   config.Config.StopSignal,
	}

	// The config's history has one entry per build step; those that produced a layer line up,
	// in order, with the manifest's layers.
	next := 0
	for _, step := range config.History {
		layer := Layer{CreatedBy: step.CreatedBy, Created: step.Created, Comment: step.Comment}
		if !step.EmptyLayer {
			if next >= len(doc.Layers) {
				next = -1
				break
			}
			layer.Digest, layer.MediaType, layer.Size = doc.Layers[next].Digest, doc.Layers[next].MediaType, doc.Layers[next].Size
			next++
		}
		platform.Layers = append(platform.Layers, layer)
	}
	if next != len(doc.Layers) { // history and layers disagree; show the layers alone
		platform.Layers = plainLayers(doc.Layers)
	}
	return platform
}

func plainLayers(descriptors []descriptor) []Layer {
	layers := make([]Layer, len(descriptors))
	for i, d := range descriptors {
		layers[i] = Layer{Digest: d.Digest, MediaType: d.MediaType, Size: d.Size}
	}
	return layers
}

func hasImageConfig(doc *manifestDoc) bool {
	return doc.Config != nil &&
		(doc.Config.MediaType == registry.MediaTypeOCIConfig || doc.Config.MediaType == registry.MediaTypeDockerConfig)
}

func platformName(d descriptor) string {
	if d.Platform == nil {
		return ""
	}
	return joinPlatform(d.Platform.OS, d.Platform.Architecture, d.Platform.Variant)
}

func joinPlatform(os, arch, variant string) string {
	parts := []string{}
	for _, part := range []string{os, arch, variant} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "/")
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
