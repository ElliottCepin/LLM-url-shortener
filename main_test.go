package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"
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

// followRedirect is a helper that issues GET /{code} against the given handler.
// It fails the test if the response isn't a 301.
func followRedirect(t *testing.T, h http.Handler, code string) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/"+code, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMovedPermanently {
		t.Fatalf("setup: GET /%s returned status %d, want 301", code, rr.Code)
	}
}

// TestRedirectToOriginalURL verifies that after shortening a URL via
// POST /shorten, issuing GET /{code} returns a 301 Moved Permanently
// response whose Location header points at the original URL.
func TestRedirectToOriginalURL(t *testing.T) {
	const originalURL = "https://example.com"

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

// TestStatsReturnsClickCountAndCreationDate verifies that GET /stats/{code}
// returns a 200 JSON response containing an accurate click count and a
// parseable creation date. After creating a shortcode and following the
// redirect three times, the reported click count must equal 3.
func TestStatsReturnsClickCountAndCreationDate(t *testing.T) {
	const originalURL = "https://example.com"
	const expectedClicks = 3

	h := handler()

	// Record roughly when the shortcode was created so we can sanity-check
	// the creation_date the server reports. Allow a small window on either
	// side to account for clock resolution and request overhead.
	before := time.Now().Add(-1 * time.Second)
	code := createShortcode(t, h, originalURL)
	after := time.Now().Add(1 * time.Second)

	for i := 0; i < expectedClicks; i++ {
		followRedirect(t, h, code)
	}

	req := httptest.NewRequest(http.MethodGet, "/stats/"+code, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var stats struct {
		Clicks       int       `json:"clicks"`
		CreationDate time.Time `json:"creation_date"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&stats); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}

	if stats.Clicks != expectedClicks {
		t.Errorf("expected %d clicks, got %d", expectedClicks, stats.Clicks)
	}

	if stats.CreationDate.Before(before) || stats.CreationDate.After(after) {
		t.Errorf("creation_date %v is outside expected window [%v, %v]",
			stats.CreationDate, before, after)
	}
}
