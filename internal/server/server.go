// Package server is the HTTP side of Regix: the JSON API the user interface calls, sign-in and
// sessions, and the user interface's own files.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strings"
	"time"

	"regix/internal/config"
	"regix/internal/inventory"
	"regix/internal/registry"
)

const (
	sessionCookie = "regix_session"
	// csrfHeader must accompany every request that changes something. A page on another site
	// cannot add a custom header without a CORS preflight, which Regix never approves.
	csrfHeader = "X-Regix-Request"

	contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
		"font-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
)

type Server struct {
	cfg      *config.Config
	registry *registry.Client
	inv      *inventory.Service
	log      *slog.Logger
	version  string
	assets   fs.FS
	sessions *sessionStore
	limiter  *loginLimiter
	users    htpasswd
}

func New(cfg *config.Config, client *registry.Client, inv *inventory.Service, log *slog.Logger, version string, assets fs.FS) (*Server, error) {
	s := &Server{
		cfg:      cfg,
		registry: client,
		inv:      inv,
		log:      log,
		version:  version,
		assets:   assets,
		sessions: newSessionStore(time.Duration(cfg.Auth.SessionTTL)),
		limiter:  newLoginLimiter(8, 5*time.Minute),
	}
	if cfg.Auth.Mode == config.AuthStatic {
		users, err := loadHtpasswd(cfg.Auth.HtpasswdFile)
		if err != nil {
			return nil, fmt.Errorf("htpasswd file: %w", err)
		}
		s.users = users
	}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /api/session", s.handleSession)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("GET /api/overview", s.authenticated(s.handleOverview))
	mux.HandleFunc("GET /api/tags", s.authenticated(s.handleTags))
	mux.HandleFunc("GET /api/image", s.authenticated(s.handleImage))
	mux.HandleFunc("POST /api/delete", s.authenticated(s.handleDelete))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})
	mux.HandleFunc("/", s.handleAsset)
	return s.protect(mux)
}

// protect applies what every response needs: security headers, the cross-site request check
// and the request log.
func (s *Server) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("Content-Security-Policy", contentSecurityPolicy)
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "same-origin")
		header.Set("Cross-Origin-Opener-Policy", "same-origin")

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			site := r.Header.Get("Sec-Fetch-Site")
			if r.Header.Get(csrfHeader) == "" || (site != "" && site != "same-origin") {
				writeError(w, http.StatusForbidden, "cross-site request refused")
				return
			}
		}
		start := time.Now()
		next.ServeHTTP(w, r)
		s.log.Debug("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}

type handler func(w http.ResponseWriter, r *http.Request, id inventory.Identity)

// authenticated resolves who is asking before the handler runs.
func (s *Server) authenticated(next handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := s.identify(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "sign in to continue")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next(w, r, id)
	}
}

// identify returns the identity behind a request, according to the auth mode.
func (s *Server) identify(r *http.Request) (inventory.Identity, bool) {
	service := registry.Credentials{Username: s.cfg.Registry.Username, Password: s.cfg.Registry.Password}
	if s.cfg.Auth.Mode == config.AuthNone {
		return inventory.Identity{User: "anonymous", Credentials: service, Scope: "service"}, true
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return inventory.Identity{}, false
	}
	current, ok := s.sessions.get(cookie.Value)
	if !ok {
		return inventory.Identity{}, false
	}
	if s.cfg.Auth.Mode == config.AuthStatic {
		return inventory.Identity{User: current.user, Credentials: service, Scope: "service"}, true
	}
	return inventory.Identity{User: current.user, Credentials: current.credentials, Scope: current.credentials.Key()}, true
}

// handleAsset serves the user interface. Paths that are not files are routes of the single-page
// app and get index.html.
func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if info, err := fs.Stat(s.assets, name); name != "" && err == nil && !info.IsDir() {
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable") // file names carry a content hash
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeFileFS(w, r, s.assets, name)
		return
	}
	if strings.HasPrefix(name, "assets/") {
		http.NotFound(w, r)
		return
	}
	index, err := fs.ReadFile(s.assets, "index.html")
	if err != nil {
		http.Error(w, "The user interface is not part of this build. Build it with `make ui`.", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(index)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, map[string]string{"error": message})
}

// fail answers a request whose work returned an error, choosing the status from the cause.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var invalid *inventory.InvalidError
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, context.Canceled):
		return // the client has gone
	case errors.As(err, &invalid):
		status = http.StatusBadRequest
	case errors.Is(err, inventory.ErrReadOnly):
		status = http.StatusForbidden
	case registry.StatusOf(err) == http.StatusUnauthorized && s.cfg.Auth.Mode == config.AuthRegistry:
		// The registry no longer accepts this user's credentials: the session is over.
		s.endSession(w, r)
		status = http.StatusUnauthorized
	case registry.StatusOf(err) == http.StatusForbidden:
		status = http.StatusForbidden
	case registry.StatusOf(err) == http.StatusNotFound:
		status = http.StatusNotFound
	}
	if status == http.StatusBadGateway {
		s.log.Error("registry request failed", "path", r.URL.Path, "error", err)
	}
	writeError(w, status, inventory.Explain(err))
}

func (s *Server) cookieSecure(r *http.Request) bool {
	switch s.cfg.Auth.CookieSecure {
	case "true":
		return true
	case "false":
		return false
	}
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
