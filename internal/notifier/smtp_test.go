package notifier

import (
	"strings"
	"testing"
)

func TestDecryptPassword(t *testing.T) {
	t.Parallel()

	got, err := decryptPassword("secret")
	if err != nil {
		t.Fatalf("decryptPassword returned error: %v", err)
	}
	if got != "secret" {
		t.Fatalf("decryptPassword = %q, want pass-through placeholder value", got)
	}

	_, err = decryptPassword("  ")
	if err == nil {
		t.Fatal("expected error for blank password")
	}
	if !strings.Contains(err.Error(), "empty smtp password") {
		t.Fatalf("error = %q, want empty smtp password", err.Error())
	}
}
