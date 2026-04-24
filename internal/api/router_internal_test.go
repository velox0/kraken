package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSanitizeFilename(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{in: " fix script.sh ", want: "fix-script.sh"},
		{in: "../../secret", want: "secret"},
		{in: "hello@#$world.cmd", want: "hello-world.cmd"},
		{in: "...---", want: ""},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := sanitizeFilename(tc.in); got != tc.want {
				t.Fatalf("sanitizeFilename(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	long := sanitizeFilename(strings.Repeat("a", 80))
	if len(long) != 64 {
		t.Fatalf("long sanitized filename length = %d, want 64", len(long))
	}
}

func TestNormalizeEmails(t *testing.T) {
	t.Parallel()

	got := normalizeEmails([]string{" USER@example.COM ", "", "ops@example.com"})
	want := []string{"user@example.com", "ops@example.com"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("email %d = %q, want %q", i, got[i], want[i])
		}
	}
	if empty := normalizeEmails(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("normalizeEmails nil = %#v, want empty non-nil slice", empty)
	}
}

func TestUptimeWindowConfig(t *testing.T) {
	t.Parallel()

	dur, bucket, err := uptimeWindowConfig("12h")
	if err != nil {
		t.Fatalf("uptimeWindowConfig returned error: %v", err)
	}
	if dur != 12*time.Hour || bucket != 5*time.Minute {
		t.Fatalf("12h config = %s/%s, want 12h/5m", dur, bucket)
	}
	if _, _, err := uptimeWindowConfig("bad"); err == nil {
		t.Fatal("expected error for bad window")
	}
}

func TestParseLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw      string
		fallback int
		want     int
	}{
		{raw: "", fallback: 25, want: 25},
		{raw: "15", fallback: 25, want: 15},
		{raw: "-1", fallback: 25, want: 25},
		{raw: "nope", fallback: 25, want: 25},
		{raw: "999", fallback: 25, want: 500},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/?limit="+tc.raw, nil)
			if got := parseLimit(req, tc.fallback); got != tc.want {
				t.Fatalf("parseLimit(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

func TestWriteJSONAndError(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusCreated, map[string]string{"status": "ok"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var payload map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("payload = %#v, want status ok", payload)
	}

	rr = httptest.NewRecorder()
	writeError(rr, http.StatusBadRequest, errors.New("bad request"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("error status = %d, want 400", rr.Code)
	}
	payload = map[string]string{}
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload["error"] != "bad request" {
		t.Fatalf("error payload = %#v, want message", payload)
	}
}

func TestAuthHelpers(t *testing.T) {
	t.Parallel()

	if !hasScope([]string{"admin"}, "anything") {
		t.Fatal("admin scope should match anything")
	}
	if !hasScope([]string{"projects:read"}, "projects:read") {
		t.Fatal("exact scope should match")
	}
	if hasScope([]string{"projects:read"}, "projects:write") {
		t.Fatal("unexpected scope match")
	}

	if !canManageUser(AuthContext{RoleLevel: 1}, 2) {
		t.Fatal("lower role level should manage higher role level")
	}
	if canManageUser(AuthContext{RoleLevel: 2}, 2) {
		t.Fatal("same role level should not manage user")
	}

	got := filterAssignableScopes([]string{"projects:read", "checks:run"}, []string{"projects:read", "admin"})
	if len(got) != 1 || got[0] != "projects:read" {
		t.Fatalf("filterAssignableScopes = %#v, want only held scope", got)
	}
	adminGot := filterAssignableScopes([]string{"admin"}, []string{"projects:read", "admin"})
	if len(adminGot) != 2 {
		t.Fatalf("admin filterAssignableScopes = %#v, want requested scopes", adminGot)
	}
}
