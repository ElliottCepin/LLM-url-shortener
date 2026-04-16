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

// createShortcode is a small helper that POSTs a URL to /shorten using the
// provided handler and returns the resulting shortcode. It fails the test
// immediately if anything goes wrong, so callers can rely on a usable code.
func createShortcode(t *testing.T, h http.Handler, url string) string {
	t.Helper()

	body := bytes.NewBufferString(`{"url": "` + url + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/shorten", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("setup: POST /shorten returned status %d, want 200", rr.Code)
	}

	respBody, err := io.ReadAll(rr.Body)
	if err != nil {
		t.Fatalf("setup: failed to read response body: %v", err)
	}

	code := string(bytes.TrimSpace(respBody))
	if code == "" {
		t.Fatal("setup: got empty shortcode from /shorten")
	}
	return code
}

// TestRedirectToOriginalURL verifies that after shortening a URL via
// POST /shorten, issuing GET /{code} returns a 301 Moved Permanently
// response whose Location header points at the original URL.
func TestRedirectToOriginalURL(t *testing.T) {
	const originalURL = "https://example.com"

	// Reuse a single handler across both requests so the in-memory store
	// persists between POST and GET.
	h := handler()

	code := createShortcode(t, h, originalURL)

	req := httptest.NewRequest(http.MethodGet, "/"+code, nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMovedPermanently {
		t.Fatalf("expected status 301, got %d", rr.Code)
	}

	if got := rr.Header().Get("Location"); got != originalURL {
		t.Errorf("expected Location header %q, got %q", originalURL, got)
	}
}
