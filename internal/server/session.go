package server

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"regix/internal/registry"
)

const maxSessions = 10000

// session is a signed-in user. Registry credentials stay here, in the server's memory: the
// browser only ever holds the random session ID.
type session struct {
	user        string
	credentials registry.Credentials
	expires     time.Time
}

// sessionStore keeps sessions in memory. They end when Regix restarts, which costs users one
// sign-in and saves a database.
type sessionStore struct {
	ttl      time.Duration
	mu       sync.Mutex
	sessions map[string]*session
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{ttl: ttl, sessions: map[string]*session{}}
}

func (s *sessionStore) create(user string, credentials registry.Credentials) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for key, existing := range s.sessions {
		if now.After(existing.expires) {
			delete(s.sessions, key)
		}
	}
	if len(s.sessions) >= maxSessions {
		return "", errors.New("too many active sessions")
	}
	s.sessions[id] = &session{user: user, credentials: credentials, expires: now.Add(s.ttl)}
	return id, nil
}

// get returns a live session and extends it: the TTL is an idle timeout.
func (s *sessionStore) get(id string) (*session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	found, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	now := time.Now()
	if now.After(found.expires) {
		delete(s.sessions, id)
		return nil, false
	}
	found.expires = now.Add(s.ttl)
	return found, true
}

func (s *sessionStore) delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

// loginLimiter slows password guessing: a few failures per user and address, then a pause.
type loginLimiter struct {
	max    int
	window time.Duration
	mu     sync.Mutex
	failed map[string]*failures
}

type failures struct {
	count int
	reset time.Time
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{max: max, window: window, failed: map[string]*failures{}}
}

func (l *loginLimiter) blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.failed[key]
	return ok && time.Now().Before(entry.reset) && entry.count >= l.max
}

func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.failed) > 10000 {
		for k, entry := range l.failed {
			if now.After(entry.reset) {
				delete(l.failed, k)
			}
		}
	}
	entry, ok := l.failed[key]
	if !ok || now.After(entry.reset) {
		entry = &failures{reset: now.Add(l.window)}
		l.failed[key] = entry
	}
	entry.count++
}

func (l *loginLimiter) succeed(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failed, key)
}

// htpasswd holds the users of the "static" auth mode: `name:bcrypt-hash` lines, as written by
// `htpasswd -B`.
type htpasswd map[string][]byte

// A hash to compare against when the user does not exist, so that an unknown name takes as long
// to reject as a wrong password.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("regix"), bcrypt.DefaultCost)

func loadHtpasswd(path string) (htpasswd, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	users := htpasswd{}
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		name, hash, ok := strings.Cut(text, ":")
		if !ok || name == "" {
			return nil, fmt.Errorf("%s line %d: expected name:hash", path, line)
		}
		if _, err := bcrypt.Cost([]byte(hash)); err != nil {
			return nil, fmt.Errorf("%s line %d: only bcrypt hashes are supported (create them with `htpasswd -B`)", path, line)
		}
		users[name] = []byte(hash)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, fmt.Errorf("%s contains no users", path)
	}
	return users, nil
}

func (h htpasswd) verify(name, password string) bool {
	hash, ok := h[name]
	if !ok {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
}
