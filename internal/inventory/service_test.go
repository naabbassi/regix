package inventory

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"regix/internal/registry"
	"regix/internal/registrytest"
)

var id = Identity{User: "tester", Scope: "test"}

func newService(t *testing.T, fake *registrytest.Fake, opts Options) *Service {
	t.Helper()
	client, err := registry.New(registry.Options{URL: fake.URL, UserAgent: "test", MaxConcurrent: 4, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if opts.CacheTTL == 0 {
		opts.CacheTTL = time.Minute
	}
	return New(client, opts, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func layer(content string, size int64) registrytest.Layer {
	return registrytest.Layer{Content: content, Size: size}
}

func tagNames(tags []Tag) []string {
	names := []string{}
	for _, tag := range tags {
		names = append(names, tag.Name)
	}
	return names
}

func TestOverviewCountsSharedLayersOnce(t *testing.T) {
	fake := registrytest.New(t)
	base := layer("base", 100000)
	fake.PushImage("api", "v1", "2026-01-01T00:00:00Z", base, layer("api-1", 10))
	fake.PushImage("api", "v2", "2026-02-01T00:00:00Z", base, layer("api-2", 20))
	fake.PushImage("web", "v1", "2026-03-01T00:00:00Z", base, layer("web-1", 300))
	fake.Tag("web", "latest", fake.PushImage("web", "v2", "2026-03-02T00:00:00Z", base))

	overview, err := newService(t, fake, Options{}).Overview(context.Background(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := overview.Totals; got.Repositories != 2 || got.Tags != 5 || got.Images != 4 {
		t.Errorf("totals = %+v, want 2 repositories, 5 tags, 4 images", got)
	}
	api, web := overview.Repositories[0], overview.Repositories[1]
	if api.Name != "api" || api.Tags != 2 || web.Tags != 3 || web.Images != 2 {
		t.Errorf("repositories = %+v", overview.Repositories)
	}
	// The base layer is in every image but stored once. Config blobs differ per image, so
	// compare against the layers alone: the totals must not count the base layer per image.
	layers := int64(100000 + 10 + 20 + 300)
	if overview.Totals.Size < layers || overview.Totals.Size >= layers+100000 {
		t.Errorf("total size = %d, want %d plus a few small config blobs", overview.Totals.Size, layers)
	}
	if want := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC); web.LastUpdated == nil || !web.LastUpdated.Equal(want) {
		t.Errorf("web last updated = %v, want %v", web.LastUpdated, want)
	}
}

func TestTagsAreNewestFirstAndCached(t *testing.T) {
	fake := registrytest.New(t)
	fake.PushImage("app", "old", "2026-01-01T00:00:00Z", layer("a", 1))
	fake.PushImage("app", "new", "2026-05-01T00:00:00Z", layer("b", 1))
	fake.PushIndex("app", "multi", "2026-03-01T00:00:00Z", layer("c", 5))
	service := newService(t, fake, Options{})

	tags, err := service.Tags(context.Background(), id, "app", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := tagNames(tags); !reflect.DeepEqual(got, []string{"new", "multi", "old"}) {
		t.Errorf("order = %v", got)
	}
	multi := tags[1]
	if multi.Kind != KindIndex || !reflect.DeepEqual(multi.Platforms, []string{"linux/amd64", "linux/arm64"}) {
		t.Errorf("multi-platform tag = %+v", multi)
	}

	before := fake.Requests()
	if _, err := service.Tags(context.Background(), id, "app", false); err != nil {
		t.Fatal(err)
	}
	if fake.Requests() != before {
		t.Error("a second listing within the TTL asked the registry again")
	}
}

func TestImageDetail(t *testing.T) {
	fake := registrytest.New(t)
	digest := fake.PushImage("team/app", "v1", "2026-01-01T00:00:00Z", layer("a", 7), layer("b", 9))
	fake.Tag("team/app", "stable", digest)

	image, err := newService(t, fake, Options{}).Image(context.Background(), id, "team/app", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if image.Digest != digest || image.Kind != KindImage || len(image.Platforms) != 1 {
		t.Fatalf("image = %+v", image)
	}
	platform := image.Platforms[0]
	if platform.Platform != "linux/amd64" || platform.Config == nil || platform.Config.Labels["repo"] != "team/app" {
		t.Errorf("platform = %+v", platform)
	}
	// Two layers with their build steps, then the CMD step that has no layer.
	if len(platform.Layers) != 3 || platform.Layers[1].Size != 9 || platform.Layers[1].CreatedBy != "RUN step-1" || platform.Layers[2].Digest != "" {
		t.Errorf("layers = %+v", platform.Layers)
	}
	if got := tagNames(image.Tags); !reflect.DeepEqual(got, []string{"stable", "v1"}) {
		t.Errorf("tags of the image = %v", got)
	}
}

func TestDelete(t *testing.T) {
	fake := registrytest.New(t)
	shared := fake.PushImage("app", "v1", "2026-01-01T00:00:00Z", layer("a", 1))
	fake.Tag("app", "stable", shared)
	single := fake.PushImage("app", "v2", "2026-02-01T00:00:00Z", layer("b", 1))
	kept := fake.PushImage("app", "release-1", "2026-03-01T00:00:00Z", layer("c", 1))
	service := newService(t, fake, Options{ProtectedTags: []string{"release-*"}})

	results, err := service.Delete(context.Background(), id, "app", []TagRef{
		{Name: "v1", Digest: shared},         // "stable" still uses the image: only the tag goes
		{Name: "v2", Digest: single},         // its only tag: the image goes
		{Name: "release-1", Digest: kept},    // protected
		{Name: "gone", Digest: single},       // does not exist
		{Name: "stable", Digest: "sha256:0"}, // moved since the caller looked
	})
	if err != nil {
		t.Fatal(err)
	}
	actions := []string{}
	for _, result := range results {
		actions = append(actions, result.Action)
	}
	want := []string{ActionUntagged, ActionImageDeleted, ActionSkipped, ActionSkipped, ActionSkipped}
	if !reflect.DeepEqual(actions, want) {
		t.Errorf("actions = %v, want %v (%+v)", actions, want, results)
	}
	if got := fake.TagsOf("app"); !reflect.DeepEqual(got, []string{"release-1", "stable"}) {
		t.Errorf("remaining tags = %v", got)
	}
	if !fake.HasManifest("app", shared) || fake.HasManifest("app", single) || !fake.HasManifest("app", kept) {
		t.Error("the wrong manifests were deleted")
	}

	// Naming every tag of an image deletes the image itself.
	if _, err := service.Delete(context.Background(), id, "app", []TagRef{{Name: "stable", Digest: shared}}); err != nil {
		t.Fatal(err)
	}
	if fake.HasManifest("app", shared) {
		t.Error("an image whose last tag was removed is still stored")
	}
	// The listing reflects the deletion at once, despite the cache.
	tags, _ := service.Tags(context.Background(), id, "app", false)
	if got := tagNames(tags); !reflect.DeepEqual(got, []string{"release-1"}) {
		t.Errorf("tags after deleting = %v", got)
	}
}

func TestDeleteRefusedWhenReadOnly(t *testing.T) {
	fake := registrytest.New(t)
	digest := fake.PushImage("app", "v1", "2026-01-01T00:00:00Z", layer("a", 1))
	service := newService(t, fake, Options{ReadOnly: true})

	_, err := service.Delete(context.Background(), id, "app", []TagRef{{Name: "v1", Digest: digest}})
	if !errors.Is(err, ErrReadOnly) || !fake.HasManifest("app", digest) {
		t.Errorf("Delete in read-only mode = %v", err)
	}
}

func TestRejectedCredentialsFailTheListing(t *testing.T) {
	fake := registrytest.New(t)
	fake.PushImage("app", "v1", "2026-01-01T00:00:00Z", layer("a", 1))
	fake.Username, fake.Password = "ci", "secret"

	_, err := newService(t, fake, Options{}).Tags(context.Background(), id, "app", false)
	if registry.StatusOf(err) != 401 {
		t.Errorf("Tags without credentials = %v, want a 401", err)
	}
}

func TestGlobMatch(t *testing.T) {
	for _, test := range []struct {
		pattern, text string
		want          bool
	}{
		{"latest", "latest", true}, {"latest", "latest2", false},
		{"v*", "v1.2", true}, {"v*", "dev", false},
		{"*-prod", "api-prod", true}, {"*-prod", "api-production", false},
		{"team/*:v*", "team/api:v1", true}, {"team/*:v*", "other/api:v1", false},
		{"a*b*c", "aXbYc", true}, {"a*b*c", "acb", false}, {"*", "anything", true},
	} {
		if got := globMatch(test.pattern, test.text); got != test.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", test.pattern, test.text, got, test.want)
		}
	}
}
