package services

import "testing"

func TestBuildEffectiveTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		checkType string
		domain    string
		route     string
		want      string
	}{
		{name: "http root", checkType: "http", domain: "example.com", route: "/", want: "example.com/"},
		{name: "http adds slash", checkType: "http", domain: "example.com/", route: "api", want: "example.com/api"},
		{name: "http preserves absolute route", checkType: "http", domain: "example.com", route: "https://other.test/health", want: "https://other.test/health"},
		{name: "http no domain", checkType: "http", domain: "", route: "/health", want: "/health"},
		{name: "tcp ignores route", checkType: "tcp", domain: "127.0.0.1:5432", route: "/ignored", want: "127.0.0.1:5432"},
		{name: "ping falls back to route", checkType: "ping", domain: "", route: "localhost", want: "localhost"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := buildEffectiveTarget(tc.checkType, tc.domain, tc.route)
			if got != tc.want {
				t.Fatalf("buildEffectiveTarget = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestServiceValidators(t *testing.T) {
	t.Parallel()

	if err := (&Worker{}).Validate(); err == nil || err.Error() != "worker store is nil" {
		t.Fatalf("Worker.Validate empty = %v, want store error", err)
	}
	if err := (&Scheduler{}).Validate(); err == nil || err.Error() != "scheduler store is nil" {
		t.Fatalf("Scheduler.Validate empty = %v, want store error", err)
	}
	if err := (&Notifier{}).Validate(); err == nil || err.Error() != "notifier store is nil" {
		t.Fatalf("Notifier.Validate empty = %v, want store error", err)
	}
}
