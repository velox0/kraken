package incident

import (
	"strings"
	"testing"
	"time"

	"github.com/velox0/kraken/internal/db"
)

func TestApplyEmailTemplateSupportsSingleAndDoubleBraces(t *testing.T) {
	t.Parallel()

	got := applyEmailTemplate("Project {project_name} / {{domain}} / {missing}", map[string]string{
		"project_name": "Kraken",
		"domain":       "example.com",
	})

	if got != "Project Kraken / example.com / {missing}" {
		t.Fatalf("applyEmailTemplate = %q", got)
	}
}

func TestTemplatesForEventUseCustomValuesAndFallbacks(t *testing.T) {
	t.Parallel()

	s := NewService(nil, nil, nil, time.Minute, EmailConfig{})
	check := db.CheckContext{
		EmailSubjectOpened:   "custom opened",
		EmailBodyOpened:      "custom body",
		EmailSubjectResolved: " ",
		EmailBodyResolved:    "resolved body",
	}

	subject, body := s.templatesForEvent(check, "opened")
	if subject != "custom opened" || body != "custom body" {
		t.Fatalf("opened templates = %q/%q, want custom values", subject, body)
	}

	subject, body = s.templatesForEvent(check, "resolved")
	if subject != defaultEmailSubjectResolved || body != "resolved body" {
		t.Fatalf("resolved templates = %q/%q, want subject fallback and custom body", subject, body)
	}

	subject, body = s.templatesForEvent(check, "repeated")
	if subject != defaultEmailSubjectRepeated || body != defaultEmailBodyRepeated {
		t.Fatalf("repeated templates = %q/%q, want defaults", subject, body)
	}
}

func TestShouldSendAlertCooldown(t *testing.T) {
	t.Parallel()

	s := NewService(nil, nil, nil, 10*time.Minute, EmailConfig{})
	if !s.shouldSendAlert(nil, true) {
		t.Fatal("new incident should send alert")
	}
	if !s.shouldSendAlert(&db.Incident{}, false) {
		t.Fatal("existing incident without last alert should send alert")
	}

	recent := time.Now().Add(-5 * time.Minute)
	if s.shouldSendAlert(&db.Incident{LastAlertSentAt: &recent}, false) {
		t.Fatal("recent alert should be throttled")
	}
	old := time.Now().Add(-15 * time.Minute)
	if !s.shouldSendAlert(&db.Incident{LastAlertSentAt: &old}, false) {
		t.Fatal("old alert should be sent")
	}
}

func TestFallbackAndTruncateForLog(t *testing.T) {
	t.Parallel()

	if got := fallback("  ", "default"); got != "default" {
		t.Fatalf("fallback blank = %q, want default", got)
	}
	if got := fallback("custom", "default"); got != "custom" {
		t.Fatalf("fallback custom = %q, want custom", got)
	}

	got := truncateForLog(strings.Repeat("a", 5), 3)
	if got != "aaa…" {
		t.Fatalf("truncateForLog = %q, want truncated with ellipsis", got)
	}
}
