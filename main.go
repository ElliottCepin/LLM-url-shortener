package main

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
)

const (
	shortcodeLen     = 7
	shortcodeAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

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
// {"url": "..."} and responds with a plain-text shortcode.
func shortenHandler(w http.ResponseWriter, r *http.Request) {
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

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(code))
}

// handler returns the top-level HTTP handler for the URL shortener.
func handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/shorten", shortenHandler)
	return mux
}

func main() {
	srv := &http.Server{
		Addr:    ":8080",
		Handler: handler(),
	}
	_ = srv.ListenAndServe()
}

