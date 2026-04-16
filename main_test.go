package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

// TestShortenReturnsValidShortcode verifies that POST /shorten with a valid URL
// in the JSON body returns a 200 response whose plain-text body matches the
// shortcode format ([a-z]|[A-Z]|[0-9])*.
func TestShortenReturnsValidShortcode(t *testing.T) {
	body := bytes.NewBufferString(`{"url": "https://example.com"}`)
	req := httptest.NewRequest(http.MethodPost, "/shorten", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	// handler() is the function that returns your *http.ServeMux (or http.Handler).
	// You'll implement this next.
	handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	respBody, err := io.ReadAll(rr.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	code := string(bytes.TrimSpace(respBody))
	if code == "" {
		t.Fatal("expected a non-empty shortcode, got empty string")
	}

	valid := regexp.MustCompile(`^[a-zA-Z0-9]+$`)
	if !valid.MatchString(code) {
		t.Errorf("shortcode %q does not match format [a-zA-Z0-9]+", code)
	}
}

