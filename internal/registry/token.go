package registry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Token ("Bearer") authentication, as used by registries that delegate to an auth server:
// the registry answers 401 with a challenge naming the token endpoint, the client trades its
// credentials for a short-lived token there and repeats the request with it.

type bearerChallenge struct {
	realm   string
	service string
	scope   string
}

type bearerToken struct {
	value   string
	expires time.Time
}

// parseBearerChallenge reads `Bearer realm="…",service="…",scope="…"`.
func parseBearerChallenge(headers []string) (bearerChallenge, bool) {
	for _, header := range headers {
		scheme, params, _ := strings.Cut(strings.TrimSpace(header), " ")
		if !strings.EqualFold(scheme, "Bearer") {
			continue
		}
		var challenge bearerChallenge
		for _, part := range splitParams(params) {
			key, value, _ := strings.Cut(part, "=")
			value = strings.Trim(strings.TrimSpace(value), `"`)
			switch strings.ToLower(strings.TrimSpace(key)) {
			case "realm":
				challenge.realm = value
			case "service":
				challenge.service = value
			case "scope":
				challenge.scope = value
			}
		}
		if challenge.realm != "" {
			return challenge, true
		}
	}
	return bearerChallenge{}, false
}

// splitParams splits on commas that are outside quotes (a scope may contain commas).
func splitParams(params string) []string {
	var parts []string
	quoted, start := false, 0
	for i, char := range params {
		switch {
		case char == '"':
			quoted = !quoted
		case char == ',' && !quoted:
			parts = append(parts, params[start:i])
			start = i + 1
		}
	}
	return append(parts, params[start:])
}

func (c *Client) fetchToken(ctx context.Context, creds Credentials, challenge bearerChallenge) (bearerToken, error) {
	realm, err := url.Parse(challenge.realm)
	if err != nil || (realm.Scheme != "http" && realm.Scheme != "https") {
		return bearerToken{}, errors.New("registry named an invalid token endpoint")
	}
	query := realm.Query()
	if challenge.service != "" {
		query.Set("service", challenge.service)
	}
	if challenge.scope != "" {
		query.Set("scope", challenge.scope)
	}
	realm.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return bearerToken{}, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	if !creds.empty() {
		req.SetBasicAuth(creds.Username, creds.Password)
	}
	res, err := c.roundTrip(req, 1<<20)
	if err != nil {
		return bearerToken{}, err
	}
	if res.status >= 400 {
		return bearerToken{}, parseError(res)
	}

	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(res.body, &body); err != nil {
		return bearerToken{}, errors.New("token endpoint sent an unreadable response")
	}
	value := body.Token
	if value == "" {
		value = body.AccessToken
	}
	if value == "" {
		return bearerToken{}, &Error{Status: http.StatusUnauthorized, Message: "token endpoint issued no token"}
	}
	if body.ExpiresIn <= 0 {
		body.ExpiresIn = 60 // the specification's default
	}
	// Stop using a token a little before it expires, so it does not expire mid-request.
	lifetime := time.Duration(body.ExpiresIn) * time.Second * 9 / 10
	return bearerToken{value: value, expires: time.Now().Add(lifetime)}, nil
}

// tokenScope names what a request needs access to; tokens are cached per scope.
func tokenScope(method, path string) string {
	path, _, _ = strings.Cut(path, "?")
	resource := "catalog"
	for _, marker := range []string{"/manifests/", "/blobs/", "/tags/list"} {
		if i := strings.Index(path, marker); i > 0 {
			resource = strings.TrimPrefix(path[:i], "/v2/")
			break
		}
	}
	if method == http.MethodDelete {
		return resource + ":delete"
	}
	return resource + ":pull"
}

func (c *Client) cachedToken(key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	token, ok := c.tokens[key]
	if !ok {
		return ""
	}
	if time.Now().After(token.expires) {
		delete(c.tokens, key)
		return ""
	}
	return token.value
}

func (c *Client) storeToken(key string, token bearerToken) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Expired tokens of users who have left would otherwise stay forever.
	if len(c.tokens) >= 4096 {
		now := time.Now()
		for k, t := range c.tokens {
			if now.After(t.expires) {
				delete(c.tokens, k)
			}
		}
	}
	c.tokens[key] = token
}
