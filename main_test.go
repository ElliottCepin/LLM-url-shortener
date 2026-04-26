package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
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

// TestLoggingMiddlewareRecordsRequests verifies that the logging middleware
// captures, for each handled request, the HTTP method, the action taken
// (create/redirect/stats), the client IP, and a success indicator. One log
// line is expected per request, and the log output must survive exercising
// all three endpoints in sequence.
func TestLoggingMiddlewareRecordsRequests(t *testing.T) {
	const originalURL = "https://example.com"
	const clientIP = "203.0.113.42"

	var logBuf bytes.Buffer
	h := handlerWithLogger(&logBuf)

	// Helper to set a predictable RemoteAddr so the IP assertion is stable.
	do := func(req *http.Request) *httptest.ResponseRecorder {
		req.RemoteAddr = clientIP + ":12345"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// 1. Create a shortcode.
	createReq := httptest.NewRequest(http.MethodPost, "/shorten",
		bytes.NewBufferString(`{"url": "`+originalURL+`"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRR := do(createReq)
	if createRR.Code != http.StatusOK {
		t.Fatalf("setup: POST /shorten returned %d", createRR.Code)
	}
	code := strings.TrimSpace(createRR.Body.String())

	// 2. Follow the redirect.
	redirectRR := do(httptest.NewRequest(http.MethodGet, "/"+code, nil))
	if redirectRR.Code != http.StatusMovedPermanently {
		t.Fatalf("setup: GET /%s returned %d", code, redirectRR.Code)
	}

	// 3. Check stats.
	statsRR := do(httptest.NewRequest(http.MethodGet, "/stats/"+code, nil))
	if statsRR.Code != http.StatusOK {
		t.Fatalf("setup: GET /stats/%s returned %d", code, statsRR.Code)
	}

	logOutput := logBuf.String()
	if logOutput == "" {
		t.Fatal("expected log output, got empty buffer")
	}

	// Expect one log line per request.
	lines := strings.Split(strings.TrimRight(logOutput, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 log lines, got %d:\n%s", len(lines), logOutput)
	}

	// Every line must contain the client IP.
	for i, line := range lines {
		if !strings.Contains(line, clientIP) {
			t.Errorf("log line %d missing client IP %q: %s", i, clientIP, line)
		}
	}

	// Line 1: shortcode creation. Must mention POST and a "create"-style action.
	if !strings.Contains(lines[0], http.MethodPost) {
		t.Errorf("line 1 missing method POST: %s", lines[0])
	}
	if !containsAny(lines[0], "create", "created", "shorten") {
		t.Errorf("line 1 missing create-style action: %s", lines[0])
	}

	// Line 2: redirect. Must mention GET and a "redirect"-style action.
	if !strings.Contains(lines[1], http.MethodGet) {
		t.Errorf("line 2 missing method GET: %s", lines[1])
	}
	if !containsAny(lines[1], "redirect", "followed") {
		t.Errorf("line 2 missing redirect-style action: %s", lines[1])
	}

	// Line 3: stats lookup. Must mention GET and a "stats"-style action.
	if !strings.Contains(lines[2], http.MethodGet) {
		t.Errorf("line 3 missing method GET: %s", lines[2])
	}
	if !containsAny(lines[2], "stats", "checked") {
		t.Errorf("line 3 missing stats-style action: %s", lines[2])
	}

	// All three actions succeeded, so every line must carry a success marker.
	for i, line := range lines {
		if !containsAny(line, "success", "true", "ok", "OK") {
			t.Errorf("log line %d missing success indicator: %s", i, line)
		}
	}
}

// containsAny reports whether s contains any of the given substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
