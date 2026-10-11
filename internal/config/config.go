// Package config loads Regix settings from defaults, an optional YAML file and environment
// variables, in that order of precedence (environment wins).
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Auth modes: who may use the UI, and whose credentials reach the registry.
const (
	// AuthRegistry: users sign in with their own registry credentials, which Regix checks
	// against the registry and uses for that user's requests.
	AuthRegistry = "registry"
	// AuthStatic: users sign in against an htpasswd file; Regix talks to the registry with the
	// configured service account.
	AuthStatic = "static"
	// AuthNone: no sign-in. Only for trusted networks or behind an authenticating proxy.
	AuthNone = "none"
)

const defaultConfigPath = "/etc/regix/config.yaml"

type Config struct {
	Listen   string   `yaml:"listen"`
	Title    string   `yaml:"title"`
	LogLevel string   `yaml:"logLevel"`
	Registry Registry `yaml:"registry"`
	Auth     Auth     `yaml:"auth"`
	// ReadOnly disables every destructive operation.
	ReadOnly bool `yaml:"readOnly"`
	// ProtectedTags are glob patterns (`*` matches anything) of tags that cannot be deleted.
	// A pattern is matched against the tag, or against `repository:tag` when it contains a colon.
	ProtectedTags []string `yaml:"protectedTags"`
	// CacheTTL is how long tag listings and the overview are reused.
	CacheTTL Duration `yaml:"cacheTTL"`
}

type Registry struct {
	// URL the Regix server uses to reach the registry, e.g. http://registry.registry.svc:5000.
	URL string `yaml:"url"`
	// Username and Password are the service account for the `static` and `none` auth modes.
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`
	PasswordFile string `yaml:"passwordFile"`
	// PullHost is the host shown in `docker pull` commands; defaults to the host of URL.
	PullHost    string `yaml:"pullHost"`
	CAFile      string `yaml:"caFile"`
	InsecureTLS bool   `yaml:"insecureTLS"`
}

type Auth struct {
	Mode         string   `yaml:"mode"`
	HtpasswdFile string   `yaml:"htpasswdFile"`
	SessionTTL   Duration `yaml:"sessionTTL"`
	// CookieSecure is "auto" (secure when the request arrived over HTTPS), "true" or "false".
	CookieSecure string `yaml:"cookieSecure"`
}

// Duration is a time.Duration that reads from YAML strings such as "30s" or "12h".
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	parsed, err := time.ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", node.Line, err)
	}
	*d = Duration(parsed)
	return nil
}

func defaults() Config {
	return Config{
		Listen:   ":8080",
		Title:    "Regix",
		LogLevel: "info",
		Auth: Auth{
			Mode:         AuthRegistry,
			SessionTTL:   Duration(12 * time.Hour),
			CookieSecure: "auto",
		},
		CacheTTL: Duration(30 * time.Second),
	}
}

// Load builds the configuration. getenv is os.Getenv outside of tests.
func Load(getenv func(string) string) (*Config, error) {
	cfg := defaults()

	path, explicit := getenv("REGIX_CONFIG"), true
	if path == "" {
		path, explicit = defaultConfigPath, false
	}
	if err := cfg.loadFile(path); err != nil && (explicit || !errors.Is(err, os.ErrNotExist)) {
		return nil, fmt.Errorf("config file %s: %w", path, err)
	}
	if err := cfg.loadEnv(getenv); err != nil {
		return nil, err
	}
	if err := cfg.finish(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) loadFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true) // a misspelled key is an error, not a silently ignored setting
	return decoder.Decode(c)
}

func (c *Config) loadEnv(getenv func(string) string) error {
	var errs []error
	str := func(key string, target *string) {
		if value := getenv(key); value != "" {
			*target = value
		}
	}
	boolean := func(key string, target *bool) {
		if value := getenv(key); value != "" {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %q is not a boolean", key, value))
			}
			*target = parsed
		}
	}
	duration := func(key string, target *Duration) {
		if value := getenv(key); value != "" {
			parsed, err := time.ParseDuration(value)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", key, err))
			}
			*target = Duration(parsed)
		}
	}

	str("REGIX_LISTEN", &c.Listen)
	str("REGIX_TITLE", &c.Title)
	str("REGIX_LOG_LEVEL", &c.LogLevel)
	str("REGIX_REGISTRY_URL", &c.Registry.URL)
	str("REGIX_REGISTRY_USERNAME", &c.Registry.Username)
	str("REGIX_REGISTRY_PASSWORD", &c.Registry.Password)
	str("REGIX_REGISTRY_PASSWORD_FILE", &c.Registry.PasswordFile)
	str("REGIX_REGISTRY_PULL_HOST", &c.Registry.PullHost)
	str("REGIX_REGISTRY_CA_FILE", &c.Registry.CAFile)
	boolean("REGIX_REGISTRY_INSECURE_TLS", &c.Registry.InsecureTLS)
	str("REGIX_AUTH_MODE", &c.Auth.Mode)
	str("REGIX_AUTH_HTPASSWD_FILE", &c.Auth.HtpasswdFile)
	duration("REGIX_AUTH_SESSION_TTL", &c.Auth.SessionTTL)
	str("REGIX_AUTH_COOKIE_SECURE", &c.Auth.CookieSecure)
	boolean("REGIX_READ_ONLY", &c.ReadOnly)
	duration("REGIX_CACHE_TTL", &c.CacheTTL)
	if value := getenv("REGIX_PROTECTED_TAGS"); value != "" {
		c.ProtectedTags = strings.Split(value, ",")
	}
	return errors.Join(errs...)
}

// finish resolves derived values and rejects configurations that cannot work.
func (c *Config) finish() error {
	if c.Registry.URL == "" {
		return errors.New("the registry URL is required: set REGIX_REGISTRY_URL or registry.url")
	}
	parsed, err := url.Parse(c.Registry.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("registry URL %q must be an http(s) URL such as https://registry.example.com", c.Registry.URL)
	}
	c.Registry.URL = strings.TrimRight(c.Registry.URL, "/")
	if c.Registry.PullHost == "" {
		c.Registry.PullHost = parsed.Host
	}

	if c.Registry.PasswordFile != "" {
		content, err := os.ReadFile(c.Registry.PasswordFile)
		if err != nil {
			return fmt.Errorf("registry password file: %w", err)
		}
		c.Registry.Password = strings.TrimRight(string(content), "\r\n")
	}

	switch c.Auth.Mode {
	case AuthRegistry, AuthNone:
	case AuthStatic:
		if c.Auth.HtpasswdFile == "" {
			return errors.New("auth mode \"static\" needs REGIX_AUTH_HTPASSWD_FILE or auth.htpasswdFile")
		}
	default:
		return fmt.Errorf("auth mode %q is not one of registry, static, none", c.Auth.Mode)
	}
	switch c.Auth.CookieSecure {
	case "auto", "true", "false":
	default:
		return fmt.Errorf("auth cookieSecure %q is not one of auto, true, false", c.Auth.CookieSecure)
	}
	if c.Auth.SessionTTL <= 0 || c.CacheTTL < 0 {
		return errors.New("sessionTTL must be positive and cacheTTL must not be negative")
	}

	patterns := c.ProtectedTags[:0]
	for _, pattern := range c.ProtectedTags {
		if pattern = strings.TrimSpace(pattern); pattern != "" {
			patterns = append(patterns, pattern)
		}
	}
	c.ProtectedTags = patterns
	return nil
}
