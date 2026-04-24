package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/velox0/kraken/internal/monitor"
)

func TestNormalizeProjectEmailTemplates(t *testing.T) {
	t.Parallel()

	p := Project{
		EmailSubjectOpened: "  ",
		EmailBodyResolved:  "custom resolved body",
	}
	normalizeProjectEmailTemplates(&p)

	if p.EmailSubjectOpened != defaultEmailSubjectOpened {
		t.Fatalf("EmailSubjectOpened = %q, want default", p.EmailSubjectOpened)
	}
	if p.EmailBodyResolved != "custom resolved body" {
		t.Fatalf("EmailBodyResolved = %q, want custom value preserved", p.EmailBodyResolved)
	}
	if p.EmailSubjectAutofixLimit != defaultEmailSubjectAutofixLimit {
		t.Fatalf("EmailSubjectAutofixLimit = %q, want default", p.EmailSubjectAutofixLimit)
	}
}

func TestNormalizeProjectParams(t *testing.T) {
	t.Parallel()

	create := CreateProjectParams{EmailSubjectRepeated: "custom"}
	normalizeCreateProjectEmailParams(&create)
	if create.EmailSubjectRepeated != "custom" {
		t.Fatalf("create custom subject overwritten: %q", create.EmailSubjectRepeated)
	}
	if create.EmailBodyOpened != defaultEmailBodyOpened {
		t.Fatalf("create EmailBodyOpened = %q, want default", create.EmailBodyOpened)
	}

	update := UpdateProjectParams{EmailBodyAutofixLimit: "custom limit body"}
	normalizeProjectEmailParams(&update)
	if update.EmailSubjectResolved != defaultEmailSubjectResolved {
		t.Fatalf("update EmailSubjectResolved = %q, want default", update.EmailSubjectResolved)
	}
	if update.EmailBodyAutofixLimit != "custom limit body" {
		t.Fatalf("update custom limit body overwritten: %q", update.EmailBodyAutofixLimit)
	}
}

func TestNormalizeCheckContextTemplates(t *testing.T) {
	t.Parallel()

	c := CheckContext{EmailSubjectOpened: "opened custom"}
	normalizeCheckContextTemplates(&c)
	if c.EmailSubjectOpened != "opened custom" {
		t.Fatalf("EmailSubjectOpened = %q, want custom value", c.EmailSubjectOpened)
	}
	if c.EmailBodyRepeated != defaultEmailBodyRepeated {
		t.Fatalf("EmailBodyRepeated = %q, want default", c.EmailBodyRepeated)
	}
}

func TestStoreScalarHelpers(t *testing.T) {
	t.Parallel()

	if got := nullInt64Arg(sql.NullInt64{Int64: 7, Valid: true}); got != int64(7) {
		t.Fatalf("nullInt64Arg valid = %#v, want 7", got)
	}
	if got := nullInt64Arg(sql.NullInt64{}); got != nil {
		t.Fatalf("nullInt64Arg invalid = %#v, want nil", got)
	}
	v := 9
	if got := nullIntArg(&v); got != 9 {
		t.Fatalf("nullIntArg = %#v, want 9", got)
	}
	if got := nullIntArg(nil); got != nil {
		t.Fatalf("nullIntArg nil = %#v, want nil", got)
	}
	if got := nullableInt(0); got != nil {
		t.Fatalf("nullableInt zero = %#v, want nil", got)
	}
	if got := nullableInt(12); got != 12 {
		t.Fatalf("nullableInt positive = %#v, want 12", got)
	}
	if got := nullableString("  hello  "); got != "hello" {
		t.Fatalf("nullableString = %#v, want trimmed string", got)
	}
	if got := nullableString(" "); got != nil {
		t.Fatalf("nullableString blank = %#v, want nil", got)
	}
	if got := clampLimit(-1, 50); got != 50 {
		t.Fatalf("clampLimit negative = %d, want fallback", got)
	}
	if got := clampLimit(600, 50); got != 500 {
		t.Fatalf("clampLimit high = %d, want 500", got)
	}
}

func TestNormalizeAndUnmarshalAssertions(t *testing.T) {
	t.Parallel()

	if got := normalizeAssertions(nil); got == nil || len(got) != 0 {
		t.Fatalf("normalizeAssertions nil = %#v, want empty non-nil slice", got)
	}
	raw := []byte(`[{"type":"status","operator":"eq","value":"200"}]`)
	got := unmarshalAssertions(raw)
	if len(got) != 1 || got[0].Type != "status" || got[0].Value != "200" {
		t.Fatalf("unmarshalAssertions = %#v, want decoded assertion", got)
	}
	if got := unmarshalAssertions([]byte(`not-json`)); got == nil || len(got) != 0 {
		t.Fatalf("unmarshalAssertions invalid = %#v, want empty non-nil slice", got)
	}
}

func TestAddTailToSlotsAndAlignToSlot(t *testing.T) {
	t.Parallel()

	origin := time.Date(2026, 4, 24, 10, 0, 0, 0, time.UTC)
	bySlot := map[time.Time]uptimeAgg{}
	addTailToSlots(bySlot, origin, 5*time.Minute, "up", origin.Add(2*time.Minute), origin.Add(8*time.Minute))

	if bySlot[origin].up != 180 {
		t.Fatalf("first slot up = %d, want 180", bySlot[origin].up)
	}
	second := origin.Add(5 * time.Minute)
	if bySlot[second].up != 180 {
		t.Fatalf("second slot up = %d, want 180", bySlot[second].up)
	}
	if got := alignToSlot(origin, 5*time.Minute, origin.Add(12*time.Minute)); !got.Equal(origin.Add(10 * time.Minute)) {
		t.Fatalf("alignToSlot = %s, want 10-minute slot", got)
	}
}

func TestProjectUptimeFreshnessWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		interval  int
		scanErr   error
		want      time.Duration
		wantError bool
	}{
		{name: "minimum", interval: 5, want: 90 * time.Second},
		{name: "normal", interval: 60, want: 240 * time.Second},
		{name: "maximum", interval: 2000, want: time.Hour},
		{name: "zero defaults", interval: 0, want: 120 * time.Second},
		{name: "missing project", scanErr: pgx.ErrNoRows, wantError: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := projectUptimeFreshnessWindow(context.Background(), fakeUptimeQuerier{
				interval: tc.interval,
				err:      tc.scanErr,
			}, 42)
			if tc.wantError {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("projectUptimeFreshnessWindow returned error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("freshness = %s, want %s", got, tc.want)
			}
		})
	}
}

type fakeUptimeQuerier struct {
	interval int
	err      error
}

func (q fakeUptimeQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	return fakeUptimeRow{interval: q.interval, err: q.err}
}

type fakeUptimeRow struct {
	interval int
	err      error
}

func (r fakeUptimeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 1 {
		return errors.New("unexpected dest count")
	}
	ptr, ok := dest[0].(*int)
	if !ok {
		return errors.New("unexpected dest type")
	}
	*ptr = r.interval
	return nil
}

func TestStoreCryptoHelpers(t *testing.T) {
	t.Parallel()

	s := &Store{}
	got, err := s.encryptEnvValue("plain")
	if err != nil {
		t.Fatalf("encrypt without crypto returned error: %v", err)
	}
	if got != "plain" {
		t.Fatalf("encrypt without crypto = %q, want pass-through", got)
	}

	c := fakeFixEnvCrypto{}
	s.SetFixEnvCrypto(c)
	got, err = s.encryptEnvValue("plain")
	if err != nil {
		t.Fatalf("encrypt with crypto returned error: %v", err)
	}
	if got != "enc:plain" {
		t.Fatalf("encrypt with crypto = %q, want encrypted value", got)
	}
	plain, err := s.decryptEnvValue(got)
	if err != nil {
		t.Fatalf("decrypt with crypto returned error: %v", err)
	}
	if plain != "plain" {
		t.Fatalf("decrypt = %q, want plaintext", plain)
	}
}

type fakeFixEnvCrypto struct{}

func (fakeFixEnvCrypto) Encrypt(plaintext string) (string, error) {
	return "enc:" + plaintext, nil
}

func (fakeFixEnvCrypto) Decrypt(ciphertext string) (string, error) {
	if len(ciphertext) < 4 || ciphertext[:4] != "enc:" {
		return "", errors.New("bad ciphertext")
	}
	return ciphertext[4:], nil
}

func TestRouteHealthSuccessRateMath(t *testing.T) {
	t.Parallel()

	item := RouteHealth{Runs1h: 4, Healthy1h: 3, Failed1h: 1, Assertions: []monitor.Assertion{}}
	if item.Runs1h > 0 {
		item.SuccessRate1h = float64(item.Healthy1h) / float64(item.Runs1h)
	}
	if item.SuccessRate1h != 0.75 {
		t.Fatalf("SuccessRate1h = %f, want 0.75", item.SuccessRate1h)
	}
}
