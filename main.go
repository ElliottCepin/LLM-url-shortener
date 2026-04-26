package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	shortcodeLen      = 7
	shortcodeAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	indexPath         = "index.html"
)

// entry is the per-shortcode record kept in the store.
type entry struct {
	url       string
	createdAt time.Time
	clicks    int
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

// indexHandler serves the static index.html file that provides the
// user-facing website.
func indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	http.ServeFile(w, r, indexPath)
}

// rootHandler dispatches requests on "/": exact "/" serves index.html,
// anything else is treated as a shortcode redirect.
func rootHandler(s *store) http.HandlerFunc {
	redirect := redirectHandler(s)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			indexHandler(w, r)
			return
		}
		redirect(w, r)
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
			Clicks       int       `json:"Clicks"`
			CreatedAt time.Time `json:"CreatedAt"`
		}{
			Clicks:       clicks,
			CreatedAt: createdAt,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// responseRecorder wraps http.ResponseWriter to capture the status code
// that was written, so the logging middleware can report it.
type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.status = code
	rr.ResponseWriter.WriteHeader(code)
}

// Write ensures that if a handler writes the body without calling
// WriteHeader explicitly, we still record the implicit 200.
func (rr *responseRecorder) Write(b []byte) (int, error) {
	if rr.status == 0 {
		rr.status = http.StatusOK
	}
	return rr.ResponseWriter.Write(b)
}

// actionFor returns a short human-readable label describing what the
// request is trying to do, based on its method and path. This is the
// "action taken" field required by the spec's logging requirements.
func actionFor(method, path string) string {
	switch {
	case method == http.MethodPost && path == "/shorten":
		return "create"
	case method == http.MethodGet && path == "/":
		return "index"
	case method == http.MethodGet && strings.HasPrefix(path, "/stats/"):
		return "stats"
	case method == http.MethodGet && path != "/" && !strings.HasPrefix(path, "/stats/"):
		return "redirect"
	default:
		return "unknown"
	}
}

// clientIP extracts the client IP from the request. It strips the port from
// RemoteAddr when present, so the log shows just the address.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// loggingMiddleware wraps a handler and writes one line per request to the
// provided writer. Each line includes method, action, time, client IP, and
// a success indicator (success=true for 2xx/3xx, false otherwise).
func loggingMiddleware(out io.Writer, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		success := rec.status >= 200 && rec.status < 400
		fmt.Fprintf(out,
			"time=%s method=%s action=%s ip=%s status=%d success=%t\n",
			time.Now().UTC().Format(time.RFC3339),
			r.Method,
			actionFor(r.Method, r.URL.Path),
			clientIP(r),
			rec.status,
			success,
		)
	})
}

// handlerWithLogger returns the top-level HTTP handler wrapped in logging
// middleware that writes to the provided writer. Each call constructs a
// fresh store, so tests get isolated state.
func handlerWithLogger(out io.Writer) http.Handler {
	s := newStore()
	mux := http.NewServeMux()
	mux.HandleFunc("/shorten", shortenHandler(s))
	mux.HandleFunc("/stats/", statsHandler(s))
	mux.HandleFunc("/", rootHandler(s))
	return loggingMiddleware(out, mux)
}

// handler returns the top-level HTTP handler for the URL shortener,
// logging to stdout. This is the production entry point; tests that want
// to inspect log output should call handlerWithLogger instead.
func handler() http.Handler {
	return handlerWithLogger(os.Stdout)
}

func main() {
	srv := &http.Server{
		Addr:    ":8080",
		Handler: handler(),
	}
	_ = srv.ListenAndServe()
}
