package monitor

import (
	"testing"
)

func TestStatusMatchesPattern(t *testing.T) {
	tests := []struct {
		code    int
		pattern string
		want    bool
	}{
		{200, "200", true},
		{200, "201", false},
		{200, "2xx", true},
		{201, "2xx", true},
		{299, "2xx", true},
		{300, "2xx", false},
		{500, "5xx", true},
		{503, "5xx", true},
		{404, "4xx", true},
		{200, "20x", true},
		{209, "20x", true},
		{210, "20x", false},
		{301, "3xx", true},
		{100, "1xx", true},
	}
	for _, tt := range tests {
		got := statusMatchesPattern(tt.code, tt.pattern)
		if got != tt.want {
			t.Errorf("statusMatchesPattern(%d, %q) = %v, want %v", tt.code, tt.pattern, got, tt.want)
		}
	}
}

func TestStatusMatchesAny(t *testing.T) {
	tests := []struct {
		code int
		csv  string
		want bool
	}{
		{200, "200,201,204", true},
		{204, "200,201,204", true},
		{400, "200,201,204", false},
		{500, "5xx,4xx", true},
		{404, "5xx,4xx", true},
		{200, "5xx,4xx", false},
		{200, "2xx", true},
	}
	for _, tt := range tests {
		got := statusMatchesAny(tt.code, tt.csv)
		if got != tt.want {
			t.Errorf("statusMatchesAny(%d, %q) = %v, want %v", tt.code, tt.csv, got, tt.want)
		}
	}
}

func TestEvaluateAssertions_NoAssertions(t *testing.T) {
	// Default: fail on 4xx/5xx for HTTP
	r200 := Result{StatusCode: 200, ResponseTimeMs: 50}
	if msg := EvaluateAssertions(nil, r200, "http"); msg != "" {
		t.Errorf("expected healthy for 200 with no assertions, got: %s", msg)
	}
	r500 := Result{StatusCode: 500, ResponseTimeMs: 50}
	if msg := EvaluateAssertions(nil, r500, "http"); msg == "" {
		t.Error("expected failure for 500 with no assertions")
	}
	// Non-HTTP: no default status check
	if msg := EvaluateAssertions(nil, r500, "tcp"); msg != "" {
		t.Errorf("expected no failure for tcp with no assertions, got: %s", msg)
	}
}

func TestEvaluateAssertions_StatusEq(t *testing.T) {
	assertions := []Assertion{{Type: "status", Operator: "eq", Value: "200"}}
	r := Result{StatusCode: 200}
	if msg := EvaluateAssertions(assertions, r, "http"); msg != "" {
		t.Errorf("expected pass, got: %s", msg)
	}
	r.StatusCode = 404
	if msg := EvaluateAssertions(assertions, r, "http"); msg == "" {
		t.Error("expected fail for 404 with eq 200")
	}
}

func TestEvaluateAssertions_StatusNeq(t *testing.T) {
	assertions := []Assertion{{Type: "status", Operator: "neq", Value: "404"}}
	r := Result{StatusCode: 200}
	if msg := EvaluateAssertions(assertions, r, "http"); msg != "" {
		t.Errorf("expected pass, got: %s", msg)
	}
	r.StatusCode = 404
	if msg := EvaluateAssertions(assertions, r, "http"); msg == "" {
		t.Error("expected fail for 404 with neq 404")
	}
}

func TestEvaluateAssertions_StatusIn(t *testing.T) {
	assertions := []Assertion{{Type: "status", Operator: "in", Value: "2xx,3xx"}}
	r := Result{StatusCode: 200}
	if msg := EvaluateAssertions(assertions, r, "http"); msg != "" {
		t.Errorf("expected pass, got: %s", msg)
	}
	r.StatusCode = 301
	if msg := EvaluateAssertions(assertions, r, "http"); msg != "" {
		t.Errorf("expected pass for 301, got: %s", msg)
	}
	r.StatusCode = 500
	if msg := EvaluateAssertions(assertions, r, "http"); msg == "" {
		t.Error("expected fail for 500 with in 2xx,3xx")
	}
}

func TestEvaluateAssertions_StatusNotIn(t *testing.T) {
	assertions := []Assertion{{Type: "status", Operator: "not_in", Value: "4xx,5xx"}}
	r := Result{StatusCode: 200}
	if msg := EvaluateAssertions(assertions, r, "http"); msg != "" {
		t.Errorf("expected pass, got: %s", msg)
	}
	r.StatusCode = 500
	if msg := EvaluateAssertions(assertions, r, "http"); msg == "" {
		t.Error("expected fail for 500 with not_in 4xx,5xx")
	}
}

func TestEvaluateAssertions_BodyRegex(t *testing.T) {
	assertions := []Assertion{{Type: "body_regex", Operator: "matches", Value: `"status"\s*:\s*"ok"`}}
	r := Result{StatusCode: 200, Body: `{"status": "ok", "data": []}`}
	if msg := EvaluateAssertions(assertions, r, "http"); msg != "" {
		t.Errorf("expected pass, got: %s", msg)
	}
	r.Body = `{"status": "error"}`
	if msg := EvaluateAssertions(assertions, r, "http"); msg == "" {
		t.Error("expected fail for body not matching pattern")
	}
}

func TestEvaluateAssertions_BodyRegexNotMatches(t *testing.T) {
	assertions := []Assertion{{Type: "body_regex", Operator: "not_matches", Value: `error`}}
	r := Result{StatusCode: 200, Body: `{"status": "ok"}`}
	if msg := EvaluateAssertions(assertions, r, "http"); msg != "" {
		t.Errorf("expected pass, got: %s", msg)
	}
	r.Body = `{"error": "something went wrong"}`
	if msg := EvaluateAssertions(assertions, r, "http"); msg == "" {
		t.Error("expected fail for body containing 'error'")
	}
}

func TestEvaluateAssertions_ResponseTime(t *testing.T) {
	assertions := []Assertion{{Type: "response_time", Operator: "lt", Value: "5000"}}
	r := Result{StatusCode: 200, ResponseTimeMs: 1000}
	if msg := EvaluateAssertions(assertions, r, "http"); msg != "" {
		t.Errorf("expected pass, got: %s", msg)
	}
	r.ResponseTimeMs = 6000
	if msg := EvaluateAssertions(assertions, r, "http"); msg == "" {
		t.Error("expected fail for 6000ms with lt 5000")
	}
}

func TestEvaluateAssertions_CustomErrorMessage(t *testing.T) {
	assertions := []Assertion{{Type: "status", Operator: "eq", Value: "200", OnFail: "Service returned non-200"}}
	r := Result{StatusCode: 500}
	msg := EvaluateAssertions(assertions, r, "http")
	if msg != "Service returned non-200" {
		t.Errorf("expected custom error message, got: %s", msg)
	}
}

func TestEvaluateAssertions_MultipleAssertions(t *testing.T) {
	assertions := []Assertion{
		{Type: "status", Operator: "in", Value: "2xx"},
		{Type: "response_time", Operator: "lt", Value: "3000"},
		{Type: "body_regex", Operator: "matches", Value: "ok"},
	}
	r := Result{StatusCode: 200, ResponseTimeMs: 500, Body: "ok fine"}
	if msg := EvaluateAssertions(assertions, r, "http"); msg != "" {
		t.Errorf("expected all pass, got: %s", msg)
	}
	// Fail on response time
	r.ResponseTimeMs = 5000
	if msg := EvaluateAssertions(assertions, r, "http"); msg == "" {
		t.Error("expected fail on response time")
	}
}

func TestEvaluateAssertions_SkipStatusForNonHTTP(t *testing.T) {
	assertions := []Assertion{
		{Type: "status", Operator: "eq", Value: "200"},
		{Type: "response_time", Operator: "lt", Value: "5000"},
	}
	r := Result{StatusCode: 0, ResponseTimeMs: 100}
	// TCP: status assertions should be skipped, but response_time applies
	if msg := EvaluateAssertions(assertions, r, "tcp"); msg != "" {
		t.Errorf("expected pass for tcp, got: %s", msg)
	}
	r.ResponseTimeMs = 6000
	if msg := EvaluateAssertions(assertions, r, "tcp"); msg == "" {
		t.Error("expected response_time fail for tcp")
	}
}
