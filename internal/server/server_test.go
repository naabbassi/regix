package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"golang.org/x/crypto/bcrypt"

	"regix/internal/config"
	"regix/internal/inventory"
	"regix/internal/registry"
	"regix/internal/registrytest"
)

var assets = fstest.MapFS{
	"index.html":    {Data: []byte("<!doctype html><title>Regix</title>")},
	"assets/app.js": {Data: []byte("console.log('regix')")},
}

// app is a running Regix in front of a fake registry, with a browser-like client.
type app struct {
	t      *testing.T
	url    string
	client *http.Client
	fake   *registrytest.Fake
}

func start(t *testing.T, configure func(cfg *config.Config, fake *registrytest.Fake)) *app {
	t.Helper()
	fake := registrytest.New(t)
	cfg := &config.Config{Title: "Test"}
	cfg.Registry.URL, cfg.Registry.PullHost = fake.URL, "registry.test"
	cfg.Auth = config.Auth{Mode: config.AuthRegistry, SessionTTL: config.Duration(time.Hour), CookieSecure: "auto"}
	configure(cfg, fake)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := registry.New(registry.Options{URL: fake.URL, UserAgent: "test", MaxConcurrent: 4, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	inv := inventory.New(client, inventory.Options{ReadOnly: cfg.ReadOnly, CacheTTL: time.Minute}, log)
	srv, err := New(cfg, client, inv, log, "test", assets)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(srv.Handler())
	t.Cleanup(httpServer.Close)
	jar, _ := cookiejar.New(nil)
	return &app{t: t, url: httpServer.URL, client: &http.Client{Jar: jar}, fake: fake}
}

// call sends a request the way the user interface does and decodes the JSON reply.
func (a *app) call(method, path, body string, headers ...string) (int, map[string]any, *http.Response) {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.url+path, strings.NewReader(body))
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(csrfHeader, "1")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i+1] == "" {
			req.Header.Del(headers[i])
		} else {
			req.Header.Set(headers[i], headers[i+1])
		}
	}
	res, err := a.client.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	return res.StatusCode, decoded, res
}

func withRegistryLogin(cfg *config.Config, fake *registrytest.Fake) {
	fake.Username, fake.Password = "ci", "secret"
	fake.PushImage("team/app", "v1", "2026-01-01T00:00:00Z", registrytest.Layer{Content: "a", Size: 42})
}

func TestRegistryModeSignInAndBrowse(t *testing.T) {
	a := start(t, withRegistryLogin)

	if status, _, _ := a.call("GET", "/api/overview", ""); status != http.StatusUnauthorized {
		t.Fatalf("overview before sign-in = %d, want 401", status)
	}
	if _, info, _ := a.call("GET", "/api/session", ""); info["authenticated"] != false || info["pullHost"] != nil {
		t.Errorf("session before sign-in = %v; it must not describe the registry", info)
	}
	if status, _, _ := a.call("POST", "/api/login", `{"username":"ci","password":"wrong"}`); status != http.StatusUnauthorized {
		t.Fatalf("sign-in with a wrong password = %d, want 401", status)
	}

	status, info, res := a.call("POST", "/api/login", `{"username":"ci","password":"secret"}`)
	if status != http.StatusOK || info["user"] != "ci" || info["pullHost"] != "registry.test" {
		t.Fatalf("sign-in = %d %v", status, info)
	}
	cookie := res.Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || strings.Contains(cookie.Value, "secret") {
		t.Errorf("session cookie = %+v", cookie)
	}

	status, overview, _ := a.call("GET", "/api/overview", "")
	if status != http.StatusOK || overview["totals"].(map[string]any)["tags"] != float64(1) {
		t.Fatalf("overview = %d %v", status, overview)
	}
	status, image, _ := a.call("GET", "/api/image?repository=team/app&reference=v1", "")
	if status != http.StatusOK || image["kind"] != "image" {
		t.Fatalf("image = %d %v", status, image)
	}
	if status, _, _ := a.call("GET", "/api/image?repository=../_catalog&reference=v1", ""); status != http.StatusBadRequest {
		t.Errorf("image with a crafted repository name = %d, want 400", status)
	}

	a.call("POST", "/api/logout", "")
	if status, _, _ := a.call("GET", "/api/overview", ""); status != http.StatusUnauthorized {
		t.Errorf("overview after sign-out = %d, want 401", status)
	}
}

func TestSessionEndsWhenRegistryRejectsCredentials(t *testing.T) {
	a := start(t, withRegistryLogin)
	a.call("POST", "/api/login", `{"username":"ci","password":"secret"}`)
	a.fake.Password = "rotated"

	if status, _, _ := a.call("GET", "/api/overview", ""); status != http.StatusUnauthorized {
		t.Fatalf("overview after the password changed = %d, want 401", status)
	}
	if _, info, _ := a.call("GET", "/api/session", ""); info["authenticated"] != false {
		t.Error("the session outlived the credentials")
	}
}

func TestCrossSiteRequestsAreRefused(t *testing.T) {
	a := start(t, withRegistryLogin)
	a.call("POST", "/api/login", `{"username":"ci","password":"secret"}`)
	body := `{"repository":"team/app","tags":[{"name":"v1","digest":"sha256:x"}]}`

	if status, _, _ := a.call("POST", "/api/delete", body, csrfHeader, ""); status != http.StatusForbidden {
		t.Errorf("delete without the request header = %d, want 403", status)
	}
	if status, _, _ := a.call("POST", "/api/delete", body, "Sec-Fetch-Site", "cross-site"); status != http.StatusForbidden {
		t.Errorf("delete from another site = %d, want 403", status)
	}
	if got := a.fake.TagsOf("team/app"); len(got) != 1 {
		t.Errorf("tags = %v; a refused request deleted something", got)
	}
}

func TestDeleteAndReadOnly(t *testing.T) {
	a := start(t, withRegistryLogin)
	a.call("POST", "/api/login", `{"username":"ci","password":"secret"}`)
	_, listing, _ := a.call("GET", "/api/tags?repository=team/app", "")
	digest := listing["tags"].([]any)[0].(map[string]any)["digest"].(string)
	body := `{"repository":"team/app","tags":[{"name":"v1","digest":"` + digest + `"}]}`

	status, reply, _ := a.call("POST", "/api/delete", body)
	if status != http.StatusOK || reply["results"].([]any)[0].(map[string]any)["action"] != "image-deleted" {
		t.Fatalf("delete = %d %v", status, reply)
	}
	if a.fake.HasManifest("team/app", digest) {
		t.Error("the image is still in the registry")
	}

	readOnly := start(t, func(cfg *config.Config, fake *registrytest.Fake) {
		withRegistryLogin(cfg, fake)
		cfg.ReadOnly = true
	})
	readOnly.call("POST", "/api/login", `{"username":"ci","password":"secret"}`)
	if status, _, _ := readOnly.call("POST", "/api/delete", body); status != http.StatusForbidden {
		t.Errorf("delete in read-only mode = %d, want 403", status)
	}
}

func TestStaticModeUsesHtpasswdAndServiceAccount(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("ui-password"), bcrypt.MinCost)
	file := filepath.Join(t.TempDir(), "htpasswd")
	if err := os.WriteFile(file, []byte("# users\nalice:"+string(hash)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := start(t, func(cfg *config.Config, fake *registrytest.Fake) {
		withRegistryLogin(cfg, fake)
		cfg.Auth.Mode, cfg.Auth.HtpasswdFile = config.AuthStatic, file
		cfg.Registry.Username, cfg.Registry.Password = "ci", "secret"
	})

	// The registry's own account is not a UI user in this mode.
	if status, _, _ := a.call("POST", "/api/login", `{"username":"ci","password":"secret"}`); status != http.StatusUnauthorized {
		t.Errorf("sign-in with the service account = %d, want 401", status)
	}
	if status, _, _ := a.call("POST", "/api/login", `{"username":"alice","password":"ui-password"}`); status != http.StatusOK {
		t.Fatalf("sign-in as alice = %d", status)
	}
	if status, _, _ := a.call("GET", "/api/overview", ""); status != http.StatusOK {
		t.Errorf("overview = %d", status)
	}
}

func TestSignInIsRateLimited(t *testing.T) {
	a := start(t, withRegistryLogin)
	last := 0
	for range 10 {
		last, _, _ = a.call("POST", "/api/login", `{"username":"ci","password":"guess"}`)
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("tenth failed sign-in = %d, want 429", last)
	}
	// Even the right password waits out the pause.
	if status, _, _ := a.call("POST", "/api/login", `{"username":"ci","password":"secret"}`); status != http.StatusTooManyRequests {
		t.Errorf("sign-in during the pause = %d, want 429", status)
	}
}

func TestNoneModeNeedsNoSignIn(t *testing.T) {
	a := start(t, func(cfg *config.Config, fake *registrytest.Fake) {
		cfg.Auth.Mode = config.AuthNone
		fake.PushImage("app", "v1", "2026-01-01T00:00:00Z", registrytest.Layer{Content: "a", Size: 1})
	})
	if status, _, _ := a.call("GET", "/api/overview", ""); status != http.StatusOK {
		t.Errorf("overview = %d", status)
	}
}

func TestUserInterfaceFiles(t *testing.T) {
	a := start(t, withRegistryLogin)
	get := func(path string) (*http.Response, string) {
		res, err := a.client.Get(a.url + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res, string(body)
	}

	for _, path := range []string{"/", "/r/team/app", "/r/team/app/-/v1"} {
		res, body := get(path)
		if res.StatusCode != http.StatusOK || !strings.Contains(body, "<title>Regix</title>") {
			t.Errorf("GET %s = %d, want the app", path, res.StatusCode)
		}
		if res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("X-Frame-Options") != "DENY" {
			t.Errorf("GET %s lacks security headers", path)
		}
	}
	if res, _ := get("/assets/app.js"); !strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Error("hashed assets should be cached for good")
	}
	if res, _ := get("/assets/missing.js"); res.StatusCode != http.StatusNotFound {
		t.Errorf("missing asset = %d, want 404", res.StatusCode)
	}
	if res, _ := get("/healthz"); res.StatusCode != http.StatusOK {
		t.Errorf("healthz = %d", res.StatusCode)
	}
}
