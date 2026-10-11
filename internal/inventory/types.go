package inventory

import (
	"encoding/json"
	"time"
)

// Kinds of things a tag can point at.
const (
	KindImage    = "image"    // a single-platform container image
	KindIndex    = "index"    // a multi-platform image
	KindArtifact = "artifact" // anything else stored in a registry, such as a Helm chart
)

// Overview is the registry at a glance.
type Overview struct {
	Repositories []RepositorySummary `json:"repositories"`
	Totals       Totals              `json:"totals"`
	GeneratedAt  time.Time           `json:"generatedAt"`
}

type Totals struct {
	Repositories int `json:"repositories"`
	Tags         int `json:"tags"`
	Images       int `json:"images"`
	// Size is the compressed size of every distinct blob the tagged images reference. Layers
	// shared between images are counted once, as the registry stores them once.
	Size int64 `json:"size"`
}

type RepositorySummary struct {
	Name        string     `json:"name"`
	Tags        int        `json:"tags"`
	Images      int        `json:"images"`
	Size        int64      `json:"size"`
	LastUpdated *time.Time `json:"lastUpdated,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// Tag is one row of a repository's tag list.
type Tag struct {
	Name      string     `json:"name"`
	Digest    string     `json:"digest,omitempty"`
	Kind      string     `json:"kind,omitempty"`
	MediaType string     `json:"mediaType,omitempty"`
	Size      int64      `json:"size"`
	Created   *time.Time `json:"created,omitempty"`
	Platforms []string   `json:"platforms,omitempty"`
	Protected bool       `json:"protected,omitempty"`
	Error     string     `json:"error,omitempty"`

	blobs []blob
}

type blob struct {
	digest string
	size   int64
}

// Image is everything known about one manifest.
type Image struct {
	Repository   string            `json:"repository"`
	Reference    string            `json:"reference"`
	Digest       string            `json:"digest"`
	MediaType    string            `json:"mediaType"`
	Kind         string            `json:"kind"`
	ArtifactType string            `json:"artifactType,omitempty"`
	Size         int64             `json:"size"`
	Created      *time.Time        `json:"created,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
	Manifest     json.RawMessage   `json:"manifest"`
	// Tags are all tags of the repository that point at this digest.
	Tags []Tag `json:"tags"`
	// Platforms holds one entry for an image, and one per platform for a multi-platform image.
	Platforms []Platform `json:"platforms"`
}

type Platform struct {
	Platform  string          `json:"platform"`
	Digest    string          `json:"digest"`
	MediaType string          `json:"mediaType"`
	Size      int64           `json:"size"`
	Created   *time.Time      `json:"created,omitempty"`
	Author    string          `json:"author,omitempty"`
	Config    *RuntimeConfig  `json:"config,omitempty"`
	Layers    []Layer         `json:"layers"`
	Manifest  json.RawMessage `json:"manifest,omitempty"`
	// Attestation marks build provenance or SBOM data attached to an image by BuildKit.
	Attestation bool   `json:"attestation,omitempty"`
	Error       string `json:"error,omitempty"`

	blobs []blob
}

type RuntimeConfig struct {
	User         string            `json:"user,omitempty"`
	WorkingDir   string            `json:"workingDir,omitempty"`
	Entrypoint   []string          `json:"entrypoint,omitempty"`
	Cmd          []string          `json:"cmd,omitempty"`
	Env          []string          `json:"env,omitempty"`
	ExposedPorts []string          `json:"exposedPorts,omitempty"`
	Volumes      []string          `json:"volumes,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	StopSignal   string            `json:"stopSignal,omitempty"`
}

// Layer is one build step. Steps that only change metadata (ENV, CMD, …) have no blob.
type Layer struct {
	Digest    string     `json:"digest,omitempty"`
	MediaType string     `json:"mediaType,omitempty"`
	Size      int64      `json:"size"`
	CreatedBy string     `json:"createdBy,omitempty"`
	Created   *time.Time `json:"created,omitempty"`
	Comment   string     `json:"comment,omitempty"`
}

// TagRef names a tag together with the digest the caller saw it at, so a tag that has been
// pushed again in the meantime is not deleted by mistake.
type TagRef struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

// Deletion outcomes.
const (
	ActionImageDeleted = "image-deleted" // the manifest is gone, and with it this tag
	ActionUntagged     = "untagged"      // only the tag is gone; other tags still use the image
	ActionSkipped      = "skipped"
)

type DeleteResult struct {
	Tag    string `json:"tag"`
	Digest string `json:"digest,omitempty"`
	Action string `json:"action"`
	Error  string `json:"error,omitempty"`
}
