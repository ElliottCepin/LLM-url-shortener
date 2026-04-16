package main

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

const (
	shortcodeLen      = 7
	shortcodeAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

// store holds the in-memory mapping from shortcode to original URL.
// All access must go through its methods so the mutex is honored.
type store struct {
	mu   sync.RWMutex
	urls map[string]string
}

func newStore() *store {
	return &store{urls: make(map[string]string)}
}

func (s *store) put(code, url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.urls[code] = url
}

func (s *store) get(code string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	url, ok := s.urls[code]
	return url, ok
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

// redirectHandler handles GET /{code}. It looks up the code in the store and
// responds with a 301 redirect to the original URL, or 404 if unknown.
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

		url, ok := s.get(code)
		if !ok {
			http.NotFound(w, r)
			return
		}

		http.Redirect(w, r, url, http.StatusMovedPermanently)
	}
}

// handler returns the top-level HTTP handler for the URL shortener.
// Each call constructs a fresh store, so tests get isolated state.
func handler() http.Handler {
	s := newStore()
	mux := http.NewServeMux()
	mux.HandleFunc("/shorten", shortenHandler(s))
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
