package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestDefaultsAndEnvironment(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"REGIX_REGISTRY_URL":   "http://registry.registry.svc:5000/",
		"REGIX_PROTECTED_TAGS": "latest, release-* ,",
		"REGIX_READ_ONLY":      "true",
		"REGIX_CACHE_TTL":      "5s",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Registry.URL != "http://registry.registry.svc:5000" || cfg.Registry.PullHost != "registry.registry.svc:5000" {
		t.Errorf("registry = %+v", cfg.Registry)
	}
	if cfg.Auth.Mode != AuthRegistry || cfg.Listen != ":8080" || !cfg.ReadOnly || time.Duration(cfg.CacheTTL) != 5*time.Second {
		t.Errorf("config = %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.ProtectedTags, []string{"latest", "release-*"}) {
		t.Errorf("protected tags = %q", cfg.ProtectedTags)
	}
}

func TestFileIsOverriddenByEnvironment(t *testing.T) {
	dir := t.TempDir()
	password := filepath.Join(dir, "password")
	file := filepath.Join(dir, "config.yaml")
	write(t, password, "s3cret\n")
	write(t, file, `
title: Company registry
registry:
  url: https://registry.example.com
  username: ui
  passwordFile: `+password+`
auth:
  mode: none
  sessionTTL: 1h
`)
	cfg, err := Load(env(map[string]string{"REGIX_CONFIG": file, "REGIX_TITLE": "From env"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Title != "From env" || cfg.Registry.Password != "s3cret" || cfg.Auth.Mode != AuthNone ||
		time.Duration(cfg.Auth.SessionTTL) != time.Hour || cfg.Registry.PullHost != "registry.example.com" {
		t.Errorf("config = %+v", cfg)
	}
}

func TestInvalidConfigurations(t *testing.T) {
	typo := filepath.Join(t.TempDir(), "config.yaml")
	write(t, typo, "registry:\n  url: http://r:5000\n  usernme: x\n")

	for name, test := range map[string]struct {
		env  map[string]string
		want string
	}{
		"no registry":    {map[string]string{}, "registry URL is required"},
		"bad URL":        {map[string]string{"REGIX_REGISTRY_URL": "registry:5000"}, "must be an http(s) URL"},
		"bad mode":       {map[string]string{"REGIX_REGISTRY_URL": "http://r", "REGIX_AUTH_MODE": "ldap"}, "not one of"},
		"static no file": {map[string]string{"REGIX_REGISTRY_URL": "http://r", "REGIX_AUTH_MODE": "static"}, "HTPASSWD"},
		"bad boolean":    {map[string]string{"REGIX_REGISTRY_URL": "http://r", "REGIX_READ_ONLY": "yes please"}, "not a boolean"},
		"missing file":   {map[string]string{"REGIX_CONFIG": "/nonexistent/regix.yaml"}, "config file"},
		"misspelled key": {map[string]string{"REGIX_CONFIG": typo}, "usernme"},
	} {
		if _, err := Load(env(test.env)); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: error = %v, want one mentioning %q", name, err, test.want)
		}
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
