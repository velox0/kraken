package monitor

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Assertion defines a single health check condition.
type Assertion struct {
	Type     string `json:"type"`               // "status", "body_regex", "response_time"
	Operator string `json:"operator"`            // "eq","neq","in","not_in","matches","not_matches","lt","gt"
	Value    string `json:"value"`               // pattern or threshold
	OnFail   string `json:"on_fail,omitempty"`   // optional custom error message
}

// Result holds the outcome of a single check execution.
type Result struct {
	Healthy        bool
	ResponseTimeMs int
	ErrorMessage   string
	StatusCode     int
	Body           string // truncated response body (HTTP only)
}

const maxBodyCapture = 64 * 1024 // 64 KB

func RunCheck(ctx context.Context, checkType, target string, timeoutMs int, assertions []Assertion) Result {
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond

	var result Result
	switch checkType {
	case "http":
		result = runHTTP(ctx, target, timeout)
	case "tcp":
		result = runTCP(ctx, target, timeout)
	case "ping":
		result = runPing(ctx, target, timeout)
	default:
		return Result{Healthy: false, ErrorMessage: "unsupported check type: " + checkType}
	}

	// If the transport itself failed (connection error, DNS, timeout), skip assertions.
	if result.ErrorMessage != "" && result.StatusCode == 0 {
		return result
	}

	// Evaluate assertions against the result.
	if err := EvaluateAssertions(assertions, result, checkType); err != "" {
		result.Healthy = false
		result.ErrorMessage = err
		return result
	}

	result.Healthy = true
	return result
}

// EvaluateAssertions runs every assertion against the result.
// Returns empty string on success, or the first failure message.
func EvaluateAssertions(assertions []Assertion, result Result, checkType string) string {
	if len(assertions) == 0 {
		// Default behavior for HTTP: fail on 4xx/5xx
		if checkType == "http" && result.StatusCode >= 400 {
			return fmt.Sprintf("status code %d", result.StatusCode)
		}
		return ""
	}

	for _, a := range assertions {
		if msg := evaluateOne(a, result, checkType); msg != "" {
			return msg
		}
	}
	return ""
}

func evaluateOne(a Assertion, result Result, checkType string) string {
	switch a.Type {
	case "status":
		return evalStatus(a, result, checkType)
	case "body_regex":
		return evalBodyRegex(a, result, checkType)
	case "response_time":
		return evalResponseTime(a, result)
	default:
		return fmt.Sprintf("unknown assertion type: %s", a.Type)
	}
}

// ---------- Status assertions ----------

func evalStatus(a Assertion, result Result, checkType string) string {
	if checkType != "http" {
		return "" // status assertions only apply to HTTP checks
	}
	code := result.StatusCode
	switch a.Operator {
	case "eq":
		if !statusMatchesPattern(code, a.Value) {
			return failMsg(a, fmt.Sprintf("expected status %s, got %d", a.Value, code))
		}
	case "neq":
		if statusMatchesPattern(code, a.Value) {
			return failMsg(a, fmt.Sprintf("expected status NOT %s, got %d", a.Value, code))
		}
	case "in":
		if !statusMatchesAny(code, a.Value) {
			return failMsg(a, fmt.Sprintf("expected status in [%s], got %d", a.Value, code))
		}
	case "not_in":
		if statusMatchesAny(code, a.Value) {
			return failMsg(a, fmt.Sprintf("expected status not in [%s], got %d", a.Value, code))
		}
	default:
		return fmt.Sprintf("unsupported status operator: %s", a.Operator)
	}
	return ""
}

// statusMatchesPattern checks if a code matches a single pattern token.
// Patterns: "200" (exact), "2xx" (200-299), "20x" (200-209), etc.
func statusMatchesPattern(code int, pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	// Try exact numeric match first.
	if n, err := strconv.Atoi(pattern); err == nil {
		return code == n
	}
	// Wildcard pattern: replace 'x'/'X' with digit ranges.
	p := strings.ToLower(pattern)
	if len(p) != 3 {
		return false
	}
	codeStr := strconv.Itoa(code)
	if len(codeStr) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		if p[i] == 'x' {
			continue // wildcard matches any digit
		}
		if p[i] != codeStr[i] {
			return false
		}
	}
	return true
}

// statusMatchesAny checks a comma-separated list of patterns.
func statusMatchesAny(code int, csv string) bool {
	for _, part := range strings.Split(csv, ",") {
		if statusMatchesPattern(code, strings.TrimSpace(part)) {
			return true
		}
	}
	return false
}

// ---------- Body regex assertions ----------

func evalBodyRegex(a Assertion, result Result, checkType string) string {
	if checkType != "http" {
		return "" // body assertions only apply to HTTP checks
	}
	re, err := regexp.Compile(a.Value)
	if err != nil {
		return failMsg(a, fmt.Sprintf("invalid body regex %q: %v", a.Value, err))
	}
	matched := re.MatchString(result.Body)
	switch a.Operator {
	case "matches":
		if !matched {
			return failMsg(a, fmt.Sprintf("body did not match pattern %q", a.Value))
		}
	case "not_matches":
		if matched {
			return failMsg(a, fmt.Sprintf("body matched forbidden pattern %q", a.Value))
		}
	default:
		return fmt.Sprintf("unsupported body_regex operator: %s", a.Operator)
	}
	return ""
}

// ---------- Response time assertions ----------

func evalResponseTime(a Assertion, result Result) string {
	threshold, err := strconv.Atoi(strings.TrimSpace(a.Value))
	if err != nil {
		return failMsg(a, fmt.Sprintf("invalid response_time threshold %q", a.Value))
	}
	switch a.Operator {
	case "lt":
		if result.ResponseTimeMs >= threshold {
			return failMsg(a, fmt.Sprintf("response time %dms >= %dms threshold", result.ResponseTimeMs, threshold))
		}
	case "gt":
		if result.ResponseTimeMs <= threshold {
			return failMsg(a, fmt.Sprintf("response time %dms <= %dms threshold", result.ResponseTimeMs, threshold))
		}
	default:
		return fmt.Sprintf("unsupported response_time operator: %s", a.Operator)
	}
	return ""
}

// ---------- Helpers ----------

func failMsg(a Assertion, defaultMsg string) string {
	if a.OnFail != "" {
		return a.OnFail
	}
	return defaultMsg
}

func runHTTP(ctx context.Context, target string, timeout time.Duration) Result {
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "http://" + target
	}
	parsed, err := url.ParseRequestURI(target)
	if err != nil {
		return Result{Healthy: false, ErrorMessage: "invalid URL target"}
	}

	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Result{Healthy: false, ErrorMessage: err.Error()}
	}

	started := time.Now()
	resp, err := client.Do(req)
	elapsed := int(time.Since(started).Milliseconds())
	if err != nil {
		return Result{Healthy: false, ResponseTimeMs: elapsed, ErrorMessage: err.Error()}
	}
	defer resp.Body.Close()

	// Read truncated body for assertion evaluation.
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyCapture))
	body := string(bodyBytes)

	return Result{
		Healthy:        true, // will be re-evaluated by assertions
		ResponseTimeMs: elapsed,
		StatusCode:     resp.StatusCode,
		Body:           body,
	}
}

func runTCP(ctx context.Context, target string, timeout time.Duration) Result {
	if _, _, err := net.SplitHostPort(target); err != nil {
		return Result{Healthy: false, ErrorMessage: "tcp target must be host:port"}
	}
	started := time.Now()
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", target)
	elapsed := int(time.Since(started).Milliseconds())
	if err != nil {
		return Result{Healthy: false, ResponseTimeMs: elapsed, ErrorMessage: err.Error()}
	}
	_ = conn.Close()
	return Result{Healthy: true, ResponseTimeMs: elapsed}
}

func runPing(ctx context.Context, target string, timeout time.Duration) Result {
	// Uses system ping so it works without raw socket privileges in the process.
	args := []string{"-c", "1", target}
	if runtime.GOOS == "linux" {
		args = []string{"-c", "1", "-W", fmt.Sprintf("%d", int(timeout.Seconds())), target}
	}

	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	cmd := exec.CommandContext(pingCtx, "ping", args...)
	output, err := cmd.CombinedOutput()
	elapsed := int(time.Since(started).Milliseconds())
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		}
		return Result{Healthy: false, ResponseTimeMs: elapsed, ErrorMessage: truncate(msg, 300)}
	}
	return Result{Healthy: true, ResponseTimeMs: elapsed}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
