package registry_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"regix/internal/registry"
	"regix/internal/registrytest"
)

func newClient(t *testing.T, url string) *registry.Client {
	t.Helper()
	client, err := registry.New(registry.Options{URL: url, UserAgent: "test", MaxConcurrent: 4, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestValidation(t *testing.T) {
	for name, want := range map[string]bool{
		"app": true, "team/app": true, "a/b/c-d_e.f": true,
		"": false, "App": false, "../etc": false, "a//b": false, "a/": false, "a?b": false, "_catalog": false,
	} {
		if got := registry.ValidRepository(name); got != want {
			t.Errorf("ValidRepository(%q) = %v, want %v", name, got, want)
		}
	}
	for tag, want := range map[string]bool{"v1.2.3": true, "latest": true, "_x": true, "": false, ".x": false, "a/b": false, "a:b": false} {
		if got := registry.ValidTag(tag); got != want {
			t.Errorf("ValidTag(%q) = %v, want %v", tag, got, want)
		}
	}
	if !registry.ValidDigest("sha256:"+"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef") || registry.ValidDigest("sha256:xyz") {
		t.Error("ValidDigest accepts or rejects the wrong digests")
	}
}

func TestListingFollowsPagination(t *testing.T) {
	fake := registrytest.New(t)
	fake.PageSize = 2
	want := []string{"a", "b", "c", "d", "e"}
	for _, repo := range want {
		fake.PushImage(repo, "v1", "2026-01-01T00:00:00Z", registrytest.Layer{Content: repo, Size: 1})
		fake.Tag(repo, "v2", fake.PushImage(repo, "v3", "2026-01-02T00:00:00Z", registrytest.Layer{Content: repo + "2", Size: 1}))
	}
	client := newClient(t, fake.URL)

	repos, err := client.Catalog(context.Background(), registry.Credentials{})
	if err != nil || !reflect.DeepEqual(repos, want) {
		t.Fatalf("Catalog = %v, %v; want %v", repos, err, want)
	}
	tags, err := client.Tags(context.Background(), registry.Credentials{}, "a")
	if err != nil || !reflect.DeepEqual(tags, []string{"v1", "v2", "v3"}) {
		t.Fatalf("Tags = %v, %v", tags, err)
	}
	if tags, err := client.Tags(context.Background(), registry.Credentials{}, "missing"); err != nil || len(tags) != 0 {
		t.Fatalf("Tags of an unknown repository = %v, %v; want none", tags, err)
	}
}

func TestBasicAuth(t *testing.T) {
	fake := registrytest.New(t)
	fake.Username, fake.Password = "ci", "secret"
	client := newClient(t, fake.URL)

	if err := client.Ping(context.Background(), registry.Credentials{Username: "ci", Password: "secret"}); err != nil {
		t.Fatalf("Ping with the right password: %v", err)
	}
	err := client.Ping(context.Background(), registry.Credentials{Username: "ci", Password: "wrong"})
	if registry.StatusOf(err) != http.StatusUnauthorized {
		t.Fatalf("Ping with a wrong password = %v, want 401", err)
	}
}

// A registry with token authentication: the client must fetch a token with its credentials and
// must not reuse one user's token for another password.
func TestBearerTokenAuth(t *testing.T) {
	tokenRequests := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenRequests++
			user, pass, _ := r.BasicAuth()
			if user != "ci" || pass != "secret" || r.URL.Query().Get("service") != "registry" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "good-token", "expires_in": 300})
		default:
			if r.Header.Get("Authorization") != "Bearer good-token" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="`+server.URL+`/token",service="registry",scope="registry:catalog:*"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"repositories": []string{"app"}})
		}
	}))
	defer server.Close()
	client := newClient(t, server.URL)
	good := registry.Credentials{Username: "ci", Password: "secret"}

	for range 2 {
		repos, err := client.Catalog(context.Background(), good)
		if err != nil || !reflect.DeepEqual(repos, []string{"app"}) {
			t.Fatalf("Catalog = %v, %v", repos, err)
		}
	}
	if tokenRequests != 1 {
		t.Errorf("token fetched %d times, want once (it should be cached)", tokenRequests)
	}
	_, err := client.Catalog(context.Background(), registry.Credentials{Username: "ci", Password: "wrong"})
	if registry.StatusOf(err) != http.StatusUnauthorized {
		t.Fatalf("Catalog with a wrong password = %v, want 401", err)
	}
}
