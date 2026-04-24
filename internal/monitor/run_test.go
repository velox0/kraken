package monitor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunCheckHTTPDefaultSchemeAndBodyAssertions(t *testing.T) {
	t.Parallel()

	server := newLocalHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(server.Close)

	target := strings.TrimPrefix(server.URL, "http://") + "/health"
	got := RunCheck(context.Background(), "http", target, 1000, []Assertion{
		{Type: "status", Operator: "eq", Value: "200"},
		{Type: "body_regex", Operator: "matches", Value: `"status":"ok"`},
	})

	if !got.Healthy {
		t.Fatalf("RunCheck healthy = false, error = %q", got.ErrorMessage)
	}
	if got.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", got.StatusCode)
	}
	if got.Body != `{"status":"ok"}` {
		t.Fatalf("Body = %q, want captured response", got.Body)
	}
}

func TestRunCheckHTTPStatusFailureDefaultsCritical(t *testing.T) {
	t.Parallel()

	server := newLocalHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "broken", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	got := RunCheck(context.Background(), "http", server.URL, 1000, nil)

	if got.Healthy {
		t.Fatal("RunCheck healthy = true, want failure")
	}
	if !got.CriticalFailure {
		t.Fatal("CriticalFailure = false, want true for default HTTP 5xx")
	}
	if got.ErrorMessage != "status code 503" {
		t.Fatalf("ErrorMessage = %q, want status failure", got.ErrorMessage)
	}
}

func TestRunCheckHTTPNonCriticalAssertionFailure(t *testing.T) {
	t.Parallel()

	server := newLocalHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(server.Close)
	critical := false

	got := RunCheck(context.Background(), "http", server.URL, 1000, []Assertion{
		{Type: "response_time", Operator: "lt", Value: "0", Critical: &critical, OnFail: "too slow for warning"},
	})

	if got.Healthy {
		t.Fatal("RunCheck healthy = true, want assertion failure")
	}
	if got.CriticalFailure {
		t.Fatal("CriticalFailure = true, want false for warning assertion")
	}
	if got.ErrorMessage != "too slow for warning" {
		t.Fatalf("ErrorMessage = %q, want custom warning", got.ErrorMessage)
	}
}

func TestRunCheckTransportFailureIsCritical(t *testing.T) {
	t.Parallel()

	got := RunCheck(context.Background(), "tcp", "127.0.0.1", 50, nil)

	if got.Healthy {
		t.Fatal("RunCheck healthy = true, want failure")
	}
	if !got.CriticalFailure {
		t.Fatal("CriticalFailure = false, want true for transport failure")
	}
	if got.ErrorMessage != "tcp target must be host:port" {
		t.Fatalf("ErrorMessage = %q, want validation failure", got.ErrorMessage)
	}
}

func TestRunCheckTCP(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		skipIfLocalListenDenied(t, err)
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	got := RunCheck(context.Background(), "tcp", ln.Addr().String(), 1000, []Assertion{
		{Type: "response_time", Operator: "lt", Value: "1000"},
	})

	if !got.Healthy {
		t.Fatalf("RunCheck TCP failed: %s", got.ErrorMessage)
	}
}

func TestRunHTTPRejectsInvalidTarget(t *testing.T) {
	t.Parallel()

	got := RunCheck(context.Background(), "http", "http://[::1", 1000, nil)

	if got.Healthy {
		t.Fatal("RunCheck healthy = true, want invalid URL failure")
	}
	if got.ErrorMessage != "invalid URL target" {
		t.Fatalf("ErrorMessage = %q, want invalid URL target", got.ErrorMessage)
	}
}

func TestRunHTTPTruncatesCapturedBody(t *testing.T) {
	t.Parallel()

	server := newLocalHTTPTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, strings.Repeat("x", maxBodyCapture+1024))
	}))
	t.Cleanup(server.Close)

	got := RunCheck(context.Background(), "http", server.URL, 1000, nil)

	if !got.Healthy {
		t.Fatalf("RunCheck failed: %s", got.ErrorMessage)
	}
	if len(got.Body) != maxBodyCapture {
		t.Fatalf("captured body length = %d, want %d", len(got.Body), maxBodyCapture)
	}
}

func TestRunCheckUnsupportedType(t *testing.T) {
	t.Parallel()

	got := RunCheck(context.Background(), "dns", "example.com", int(time.Second/time.Millisecond), nil)

	if got.Healthy {
		t.Fatal("RunCheck healthy = true, want unsupported failure")
	}
	if got.ErrorMessage != "unsupported check type: dns" {
		t.Fatalf("ErrorMessage = %q, want unsupported check type", got.ErrorMessage)
	}
}

func newLocalHTTPTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		skipIfLocalListenDenied(t, err)
		t.Fatal(err)
	}

	server := httptest.NewUnstartedServer(handler)
	server.Listener = ln
	server.Start()
	return server
}

func skipIfLocalListenDenied(t *testing.T, err error) {
	t.Helper()
	if strings.Contains(err.Error(), "operation not permitted") {
		t.Skipf("local listener unavailable in this environment: %v", err)
	}
}
