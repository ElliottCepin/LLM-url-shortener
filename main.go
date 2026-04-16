package main

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	shortcodeLen      = 7
	shortcodeAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

// entry is the per-shortcode record kept in the store.
type entry struct {
	url          string
	createdAt    time.Time
	clicks       int
}

// store holds the in-memory mapping from shortcode to entry.
// All access must go through its methods so the mutex is honored.
type store struct {
	mu      sync.RWMutex
	entries map[string]*entry
}

func newStore() *store {
	return &store{entries: make(map[string]*entry)}
}

func (s *store) put(code, url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[code] = &entry{
		url:       url,
		createdAt: time.Now(),
	}
}

// resolve looks up a code, increments its click counter, and returns the URL.
// The increment happens atomically with the lookup so concurrent redirects
// can't lose counts.
func (s *store) resolve(code string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[code]
	if !ok {
		return "", false
	}
	e.clicks++
	return e.url, true
}

// stats returns a snapshot of the click count and creation time for a code.
func (s *store) stats(code string) (clicks int, createdAt time.Time, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, exists := s.entries[code]
	if !exists {
		return 0, time.Time{}, false
	}
	return e.clicks, e.createdAt, true
}

// generateShortcode returns a random string of shortcodeLen characters
// drawn from shortcodeAlphabet. Matches the spec format ([a-z]|[A-Z]|[0-9])*.
func generateShortcode() (string, error) {
	buf := make([]byte, shortcodeLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		buf[i] = shortcodeAlphabet[int(b)%len(shortcodeAlphabet)]
	}
	return string(buf), nil
}

// shortenHandler handles POST /shorten. It decodes a JSON body of the form
// {"url": "..."} and responds with a plain-text shortcode, recording the
// mapping in the provided store.
func shortenHandler(s *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var payload struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}

		code, err := generateShortcode()
		if err != nil {
			http.Error(w, "failed to generate shortcode", http.StatusInternalServerError)
			return
		}

		s.put(code, payload.URL)

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(code))
	}
}

// redirectHandler handles GET /{code}. It looks up the code in the store,
// increments its click counter, and responds with a 301 redirect to the
// original URL, or 404 if unknown.
func redirectHandler(s *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		code := strings.TrimPrefix(r.URL.Path, "/")
		if code == "" {
			http.NotFound(w, r)
			return
		}

		url, ok := s.resolve(code)
		if !ok {
			http.NotFound(w, r)
			return
		}

		http.Redirect(w, r, url, http.StatusMovedPermanently)
	}
}

// statsHandler handles GET /stats/{code}. It responds with a JSON body
// containing the click count and creation date for the given shortcode,
// or 404 if the code is unknown.
func statsHandler(s *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		code := strings.TrimPrefix(r.URL.Path, "/stats/")
		if code == "" || strings.Contains(code, "/") {
			http.NotFound(w, r)
			return
		}

		clicks, createdAt, ok := s.stats(code)
		if !ok {
			http.NotFound(w, r)
			return
		}

		resp := struct {
			Clicks       int       `json:"clicks"`
			CreationDate time.Time `json:"creation_date"`
		}{
			Clicks:       clicks,
			CreationDate: createdAt,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// handler returns the top-level HTTP handler for the URL shortener.
// Each call constructs a fresh store, so tests get isolated state.
func handler() http.Handler {
	s := newStore()
	mux := http.NewServeMux()
	mux.HandleFunc("/shorten", shortenHandler(s))
	mux.HandleFunc("/stats/", statsHandler(s))
	mux.HandleFunc("/", redirectHandler(s))
	return mux
}

func main() {
	srv := &http.Server{
		Addr:    ":8080",
		Handler: handler(),
	}
	_ = srv.ListenAndServe()
}
