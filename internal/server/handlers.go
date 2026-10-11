package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"regix/internal/config"
	"regix/internal/inventory"
	"regix/internal/registry"
)

// sessionInfo is what the user interface needs to know before it shows anything.
type sessionInfo struct {
	Title         string `json:"title"`
	Version       string `json:"version"`
	AuthMode      string `json:"authMode"`
	Authenticated bool   `json:"authenticated"`
	User          string `json:"user,omitempty"`
	// The rest describes the registry and is only told to signed-in users.
	PullHost string `json:"pullHost,omitempty"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

func (s *Server) sessionInfo(r *http.Request) sessionInfo {
	info := sessionInfo{Title: s.cfg.Title, Version: s.version, AuthMode: s.cfg.Auth.Mode}
	if id, ok := s.identify(r); ok {
		info.Authenticated = true
		info.User = id.User
		info.PullHost = s.cfg.Registry.PullHost
		info.ReadOnly = s.cfg.ReadOnly
	}
	return info
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.sessionInfo(r))
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Auth.Mode == config.AuthNone {
		writeError(w, http.StatusBadRequest, "sign-in is switched off")
		return
	}
	var form struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, 8<<10, &form) {
		return
	}
	if form.Username == "" || form.Password == "" || len(form.Username) > 256 || len(form.Password) > 1024 {
		writeError(w, http.StatusBadRequest, "enter a username and a password")
		return
	}

	limitKey := clientAddress(r) + "|" + strings.ToLower(form.Username)
	if s.limiter.blocked(limitKey) {
		writeError(w, http.StatusTooManyRequests, "too many failed attempts; wait a few minutes and try again")
		return
	}

	credentials := registry.Credentials{Username: form.Username, Password: form.Password}
	accepted := false
	if s.cfg.Auth.Mode == config.AuthStatic {
		accepted = s.users.verify(form.Username, form.Password)
		credentials = registry.Credentials{} // the service account is used, not the user's password
	} else {
		err := s.registry.Ping(r.Context(), credentials)
		switch registry.StatusOf(err) {
		case http.StatusUnauthorized, http.StatusForbidden:
		default:
			if err != nil {
				s.log.Error("sign-in could not reach the registry", "error", err)
				writeError(w, http.StatusBadGateway, inventory.Explain(err))
				return
			}
			accepted = true
		}
	}
	if !accepted {
		s.limiter.fail(limitKey)
		s.log.Warn("sign-in failed", "user", form.Username, "address", clientAddress(r))
		writeError(w, http.StatusUnauthorized, "wrong username or password")
		return
	}
	s.limiter.succeed(limitKey)

	id, err := s.sessions.create(form.Username, credentials)
	if err != nil {
		s.log.Error("session could not be created", "error", err)
		writeError(w, http.StatusServiceUnavailable, "could not sign in right now")
		return
	}
	cookie := &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		MaxAge:   int(time.Duration(s.cfg.Auth.SessionTTL).Seconds()),
		HttpOnly: true,
		Secure:   s.cookieSecure(r),
		SameSite: http.SameSiteStrictMode,
	}
	http.SetCookie(w, cookie)
	s.log.Info("signed in", "user", form.Username, "address", clientAddress(r))

	r.AddCookie(cookie) // so the reply describes the session that was just created
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.sessionInfo(r))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.endSession(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) endSession(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.delete(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.cookieSecure(r), SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request, id inventory.Identity) {
	overview, err := s.inv.Overview(r.Context(), id, r.URL.Query().Has("fresh"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request, id inventory.Identity) {
	query := r.URL.Query()
	tags, err := s.inv.Tags(r.Context(), id, query.Get("repository"), query.Has("fresh"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if tags == nil {
		tags = []inventory.Tag{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"repository": query.Get("repository"), "tags": tags})
}

func (s *Server) handleImage(w http.ResponseWriter, r *http.Request, id inventory.Identity) {
	query := r.URL.Query()
	image, err := s.inv.Image(r.Context(), id, query.Get("repository"), query.Get("reference"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, image)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request, id inventory.Identity) {
	var request struct {
		Repository string             `json:"repository"`
		Tags       []inventory.TagRef `json:"tags"`
	}
	if !readJSON(w, r, 1<<20, &request) {
		return
	}
	results, err := s.inv.Delete(r.Context(), id, request.Repository, request.Tags)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// readJSON decodes a JSON request body of bounded size, answering the request itself when the
// body is not acceptable.
func readJSON(w http.ResponseWriter, r *http.Request, limit int64, target any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "send JSON")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "the request body is not valid")
		return false
	}
	return true
}
