// Package registry is a small client for the OCI Distribution API (Docker Registry v2/v3 HTTP
// API). It knows nothing about the UI: it sends authenticated requests and returns what the
// registry said.
package registry

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	MediaTypeOCIManifest    = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeOCIIndex       = "application/vnd.oci.image.index.v1+json"
	MediaTypeDockerManifest = "application/vnd.docker.distribution.manifest.v2+json"
	MediaTypeDockerList     = "application/vnd.docker.distribution.manifest.list.v2+json"
	MediaTypeOCIConfig      = "application/vnd.oci.image.config.v1+json"
	MediaTypeDockerConfig   = "application/vnd.docker.container.image.v1+json"

	manifestAccept = MediaTypeOCIManifest + ", " + MediaTypeDockerManifest + ", " +
		MediaTypeOCIIndex + ", " + MediaTypeDockerList

	pageSize       = 1000
	maxPages       = 1000
	maxListBytes   = 8 << 20
	maxManifestLen = 4 << 20
	// MaxConfigBytes bounds an image config blob; real ones are a few kilobytes.
	MaxConfigBytes = 4 << 20
)

// Names are validated before they are put into a URL path, so a crafted repository or reference
// cannot reach another endpoint of the registry.
var (
	repositoryPattern = regexp.MustCompile(`^[a-z0-9]+(?:(?:\.|_|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:\.|_|__|-+)[a-z0-9]+)*)*$`)
	tagPattern        = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	digestPattern     = regexp.MustCompile(`^[a-z0-9]+(?:[+._-][a-z0-9]+)*:[a-fA-F0-9]{32,128}$`)
)

func ValidRepository(name string) bool {
	return len(name) <= 255 && repositoryPattern.MatchString(name)
}
func ValidTag(tag string) bool       { return tagPattern.MatchString(tag) }
func ValidDigest(digest string) bool { return digestPattern.MatchString(digest) }

// Credentials are a registry username and password. The zero value means anonymous access.
type Credentials struct {
	Username string
	Password string
}

func (c Credentials) empty() bool { return c.Username == "" && c.Password == "" }

// Key identifies the credentials without revealing them. It depends on the password too, so
// that what was cached for the right password is never served for a wrong one.
func (c Credentials) Key() string {
	sum := sha256.Sum256([]byte(c.Username + "\x00" + c.Password))
	return hex.EncodeToString(sum[:])
}

// Error is an error response from the registry.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("registry responded %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("registry responded %d", e.Status)
}

// StatusOf returns the HTTP status of a registry error, or 0 for any other error.
func StatusOf(err error) int {
	var regErr *Error
	if errors.As(err, &regErr) {
		return regErr.Status
	}
	return 0
}

type Options struct {
	URL         string
	CAFile      string
	InsecureTLS bool
	UserAgent   string
	// MaxConcurrent bounds the requests in flight, so listing a big registry cannot flood it.
	MaxConcurrent int
	Timeout       time.Duration
}

type Client struct {
	base      string
	http      *http.Client
	userAgent string
	slots     chan struct{}

	mu     sync.Mutex
	tokens map[string]bearerToken
}

func New(opts Options) (*Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = opts.MaxConcurrent
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: opts.InsecureTLS}
	if opts.CAFile != "" {
		pem, err := os.ReadFile(opts.CAFile)
		if err != nil {
			return nil, fmt.Errorf("registry CA file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("registry CA file %s contains no certificates", opts.CAFile)
		}
		transport.TLSClientConfig.RootCAs = pool
	}
	return &Client{
		base:      strings.TrimRight(opts.URL, "/"),
		http:      &http.Client{Transport: transport, Timeout: opts.Timeout},
		userAgent: opts.UserAgent,
		slots:     make(chan struct{}, opts.MaxConcurrent),
		tokens:    map[string]bearerToken{},
	}, nil
}

// Ping checks that the registry is reachable and accepts the credentials.
func (c *Client) Ping(ctx context.Context, creds Credentials) error {
	_, err := c.do(ctx, creds, http.MethodGet, "/v2/", "", 1<<20)
	return err
}

// Catalog lists every repository the credentials may see.
func (c *Client) Catalog(ctx context.Context, creds Credentials) ([]string, error) {
	return c.list(ctx, creds, "/v2/_catalog", "repositories")
}

// Tags lists the tags of a repository. A repository the registry does not know has no tags.
func (c *Client) Tags(ctx context.Context, creds Credentials, repo string) ([]string, error) {
	tags, err := c.list(ctx, creds, "/v2/"+repo+"/tags/list", "tags")
	if StatusOf(err) == http.StatusNotFound {
		return nil, nil
	}
	return tags, err
}

// list reads a paginated listing, following the registry's `Link: <…>; rel="next"` headers.
func (c *Client) list(ctx context.Context, creds Credentials, path, field string) ([]string, error) {
	var all []string
	next := fmt.Sprintf("%s?n=%d", path, pageSize)
	for page := 0; next != "" && page < maxPages; page++ {
		res, err := c.do(ctx, creds, http.MethodGet, next, "", maxListBytes)
		if err != nil {
			return nil, err
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(res.body, &body); err != nil {
			return nil, fmt.Errorf("registry sent an unreadable listing: %w", err)
		}
		var items []string
		if raw, ok := body[field]; ok {
			if err := json.Unmarshal(raw, &items); err != nil {
				return nil, fmt.Errorf("registry sent an unreadable listing: %w", err)
			}
		}
		all = append(all, items...)
		next = nextPage(res.header.Get("Link"))
	}
	return all, nil
}

// nextPage returns the path and query of a Link header's target. The scheme and host are
// dropped on purpose: the next page is always requested from the configured registry.
func nextPage(link string) string {
	start, end := strings.Index(link, "<"), strings.Index(link, ">")
	if start < 0 || end < start || !strings.Contains(link[end:], `rel="next"`) {
		return ""
	}
	target, err := url.Parse(link[start+1 : end])
	if err != nil || !strings.HasPrefix(target.Path, "/v2/") {
		return ""
	}
	return target.RequestURI()
}

// Manifest is a manifest as the registry stores it.
type Manifest struct {
	Digest    string
	MediaType string
	Raw       []byte
}

// Digest resolves a tag (or checks a digest) without downloading the manifest.
func (c *Client) Digest(ctx context.Context, creds Credentials, repo, reference string) (string, error) {
	res, err := c.do(ctx, creds, http.MethodHead, manifestPath(repo, reference), manifestAccept, 0)
	if err != nil {
		return "", err
	}
	digest := res.header.Get("Docker-Content-Digest")
	if !ValidDigest(digest) {
		return "", errors.New("registry did not report a manifest digest")
	}
	return digest, nil
}

func (c *Client) Manifest(ctx context.Context, creds Credentials, repo, reference string) (*Manifest, error) {
	res, err := c.do(ctx, creds, http.MethodGet, manifestPath(repo, reference), manifestAccept, maxManifestLen)
	if err != nil {
		return nil, err
	}
	digest := res.header.Get("Docker-Content-Digest")
	if !ValidDigest(digest) {
		sum := sha256.Sum256(res.body)
		digest = "sha256:" + hex.EncodeToString(sum[:])
	}
	mediaType, _, _ := strings.Cut(res.header.Get("Content-Type"), ";")
	return &Manifest{Digest: digest, MediaType: strings.TrimSpace(mediaType), Raw: res.body}, nil
}

// Blob downloads a small blob such as an image config.
func (c *Client) Blob(ctx context.Context, creds Credentials, repo, digest string, limit int64) ([]byte, error) {
	res, err := c.do(ctx, creds, http.MethodGet, "/v2/"+repo+"/blobs/"+digest, "", limit)
	if err != nil {
		return nil, err
	}
	return res.body, nil
}

// DeleteManifest removes a manifest by digest, and with it every tag that points at it.
// The registry frees the storage only when its garbage collector runs.
func (c *Client) DeleteManifest(ctx context.Context, creds Credentials, repo, digest string) error {
	_, err := c.do(ctx, creds, http.MethodDelete, manifestPath(repo, digest), "", 1<<20)
	return err
}

// DeleteTag removes one tag and leaves the manifest in place (Distribution v3, OCI 1.1).
func (c *Client) DeleteTag(ctx context.Context, creds Credentials, repo, tag string) error {
	_, err := c.do(ctx, creds, http.MethodDelete, manifestPath(repo, tag), "", 1<<20)
	return err
}

func manifestPath(repo, reference string) string {
	return "/v2/" + repo + "/manifests/" + reference
}

type response struct {
	status int
	header http.Header
	body   []byte
}

// do sends one request, answering a Bearer challenge by fetching a token and retrying once.
// The whole (small) body is read here so a concurrency slot is held only while the request runs.
func (c *Client) do(ctx context.Context, creds Credentials, method, path, accept string, limit int64) (*response, error) {
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	tokenKey := creds.Key() + "|" + tokenScope(method, path)
	res, err := c.send(ctx, creds, c.cachedToken(tokenKey), method, path, accept, limit)
	if err != nil {
		return nil, err
	}
	if res.status == http.StatusUnauthorized {
		if challenge, ok := parseBearerChallenge(res.header.Values("WWW-Authenticate")); ok {
			token, err := c.fetchToken(ctx, creds, challenge)
			if err != nil {
				return nil, err
			}
			c.storeToken(tokenKey, token)
			if res, err = c.send(ctx, creds, token.value, method, path, accept, limit); err != nil {
				return nil, err
			}
		}
	}
	if res.status >= 400 {
		return nil, parseError(res)
	}
	return res, nil
}

func (c *Client) send(ctx context.Context, creds Credentials, bearer, method, path, accept string, limit int64) (*response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("User-Agent", c.userAgent)
	switch {
	case bearer != "":
		req.Header.Set("Authorization", "Bearer "+bearer)
	case !creds.empty():
		req.SetBasicAuth(creds.Username, creds.Password)
	}
	return c.roundTrip(req, limit)
}

func (c *Client) roundTrip(req *http.Request, limit int64) (*response, error) {
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("registry response to %s %s is larger than %d bytes", req.Method, req.URL.Path, limit)
	}
	return &response{status: res.StatusCode, header: res.Header, body: body}, nil
}

func parseError(res *response) error {
	regErr := &Error{Status: res.status}
	var body struct {
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(res.body, &body) == nil && len(body.Errors) > 0 {
		regErr.Code, regErr.Message = body.Errors[0].Code, body.Errors[0].Message
	}
	return regErr
}
