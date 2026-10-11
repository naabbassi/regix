// Package registrytest provides an in-memory registry for tests: enough of the Distribution API
// to list, inspect and delete images.
package registrytest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

const (
	manifestType = "application/vnd.oci.image.manifest.v1+json"
	indexType    = "application/vnd.oci.image.index.v1+json"
	configType   = "application/vnd.oci.image.config.v1+json"
)

// Fake is a registry holding the images pushed with its Push methods.
type Fake struct {
	*httptest.Server
	// Username and Password, when set, are required by every request (basic auth).
	Username, Password string
	// PageSize, when set, paginates listings so pagination can be tested.
	PageSize int

	mu        sync.Mutex
	manifests map[string]map[string]stored // repository -> digest -> manifest
	tags      map[string]map[string]string // repository -> tag -> digest
	blobs     map[string][]byte
	requests  int
}

type stored struct {
	mediaType string
	raw       []byte
}

func New(t *testing.T) *Fake {
	t.Helper()
	fake := &Fake{
		manifests: map[string]map[string]stored{},
		tags:      map[string]map[string]string{},
		blobs:     map[string][]byte{},
	}
	fake.Server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.Close)
	return fake
}

// Layer describes a layer to push: its content stands in for the blob and sets its digest.
type Layer struct {
	Content string
	Size    int64
}

// PushImage stores a single-platform image and tags it. It returns the manifest digest.
func (f *Fake) PushImage(repo, tag, created string, layers ...Layer) string {
	digest, _ := f.pushManifest(repo, "linux", "amd64", created, layers)
	f.Tag(repo, tag, digest)
	return digest
}

// PushIndex stores a two-platform image (linux/amd64 and linux/arm64) and tags it.
func (f *Fake) PushIndex(repo, tag, created string, layers ...Layer) string {
	var members []map[string]any
	for _, arch := range []string{"amd64", "arm64"} {
		digest, size := f.pushManifest(repo, "linux", arch, created, layers)
		members = append(members, map[string]any{
			"mediaType": manifestType, "digest": digest, "size": size,
			"platform": map[string]string{"os": "linux", "architecture": arch},
		})
	}
	raw, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": indexType, "manifests": members})
	digest := f.store(repo, indexType, raw)
	f.Tag(repo, tag, digest)
	return digest
}

func (f *Fake) pushManifest(repo, os, arch, created string, layers []Layer) (string, int) {
	history := []map[string]any{}
	descriptors := []map[string]any{}
	for i, layer := range layers {
		history = append(history, map[string]any{"created": created, "created_by": fmt.Sprintf("RUN step-%d", i)})
		descriptors = append(descriptors, map[string]any{
			"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": digestOf([]byte(layer.Content)), "size": layer.Size,
		})
	}
	history = append(history, map[string]any{"created": created, "created_by": `CMD ["app"]`, "empty_layer": true})
	config, _ := json.Marshal(map[string]any{
		"created": created, "os": os, "architecture": arch, "history": history,
		"config": map[string]any{"Cmd": []string{"app"}, "Env": []string{"PATH=/bin"}, "Labels": map[string]string{"repo": repo}},
	})
	f.mu.Lock()
	f.blobs[digestOf(config)] = config
	f.mu.Unlock()

	raw, _ := json.Marshal(map[string]any{
		"schemaVersion": 2, "mediaType": manifestType,
		"config": map[string]any{"mediaType": configType, "digest": digestOf(config), "size": len(config)},
		"layers": descriptors,
	})
	return f.store(repo, manifestType, raw), len(raw)
}

func (f *Fake) store(repo, mediaType string, raw []byte) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.manifests[repo] == nil {
		f.manifests[repo] = map[string]stored{}
		f.tags[repo] = map[string]string{}
	}
	digest := digestOf(raw)
	f.manifests[repo][digest] = stored{mediaType, raw}
	return digest
}

// Tag points a tag at a manifest that is already stored.
func (f *Fake) Tag(repo, tag, digest string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tags[repo][tag] = digest
}

// TagsOf returns the tags of a repository, sorted.
func (f *Fake) TagsOf(repo string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	tags := []string{}
	for tag := range f.tags[repo] {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

// HasManifest reports whether a manifest is still stored.
func (f *Fake) HasManifest(repo, digest string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.manifests[repo][digest]
	return ok
}

// Requests returns how many requests the registry has served.
func (f *Fake) Requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

func digestOf(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++

	if f.Username != "" {
		user, pass, ok := r.BasicAuth()
		if !ok || user != f.Username || pass != f.Password {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			fail(w, http.StatusUnauthorized, "UNAUTHORIZED")
			return
		}
	}

	path := strings.TrimPrefix(r.URL.Path, "/v2/")
	switch {
	case r.URL.Path == "/v2/":
		w.WriteHeader(http.StatusOK)
	case path == "_catalog":
		names := []string{}
		for repo := range f.manifests {
			names = append(names, repo)
		}
		f.page(w, r, "repositories", names, nil)
	case strings.HasSuffix(path, "/tags/list"):
		repo := strings.TrimSuffix(path, "/tags/list")
		if _, ok := f.manifests[repo]; !ok {
			fail(w, http.StatusNotFound, "NAME_UNKNOWN")
			return
		}
		names := []string{}
		for tag := range f.tags[repo] {
			names = append(names, tag)
		}
		f.page(w, r, "tags", names, map[string]any{"name": repo})
	case strings.Contains(path, "/manifests/"):
		repo, reference, _ := strings.Cut(path, "/manifests/")
		f.serveManifest(w, r, repo, reference)
	case strings.Contains(path, "/blobs/"):
		_, digest, _ := strings.Cut(path, "/blobs/")
		content, ok := f.blobs[digest]
		if !ok {
			fail(w, http.StatusNotFound, "BLOB_UNKNOWN")
			return
		}
		_, _ = w.Write(content)
	default:
		fail(w, http.StatusNotFound, "NOT_FOUND")
	}
}

func (f *Fake) serveManifest(w http.ResponseWriter, r *http.Request, repo, reference string) {
	digest, byTag := reference, false
	if !strings.Contains(reference, ":") {
		digest, byTag = f.tags[repo][reference], true
	}
	manifest, ok := f.manifests[repo][digest]
	if !ok {
		fail(w, http.StatusNotFound, "MANIFEST_UNKNOWN")
		return
	}
	if r.Method == http.MethodDelete {
		if byTag {
			delete(f.tags[repo], reference)
		} else {
			delete(f.manifests[repo], digest)
			for tag, target := range f.tags[repo] {
				if target == digest {
					delete(f.tags[repo], tag)
				}
			}
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", manifest.mediaType)
	w.Header().Set("Docker-Content-Digest", digest)
	if r.Method != http.MethodHead {
		_, _ = w.Write(manifest.raw)
	}
}

// page writes a listing, split into pages with Link headers when PageSize is set.
func (f *Fake) page(w http.ResponseWriter, r *http.Request, field string, names []string, extra map[string]any) {
	sort.Strings(names)
	if last := r.URL.Query().Get("last"); last != "" {
		names = names[sort.SearchStrings(names, last)+1:]
	}
	if f.PageSize > 0 && len(names) > f.PageSize {
		names = names[:f.PageSize]
		w.Header().Set("Link", fmt.Sprintf(`<%s?last=%s&n=%d>; rel="next"`, r.URL.Path, names[len(names)-1], f.PageSize))
	}
	body := map[string]any{field: names}
	for key, value := range extra {
		body[key] = value
	}
	_ = json.NewEncoder(w).Encode(body)
}

func fail(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"code": code, "message": strings.ToLower(code)}}})
}
