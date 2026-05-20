package config

import (
	"os"
	"runtime"
	"testing"
)

func TestLoadUsesDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	clearConfigEnv(t)

	got := Load()

	if got.APIAddr != ":8080" {
		t.Fatalf("APIAddr = %q, want default", got.APIAddr)
	}
	if got.RedisDB != 0 {
		t.Fatalf("RedisDB = %d, want 0", got.RedisDB)
	}
	if got.SchedulerTickSec != 2 {
		t.Fatalf("SchedulerTickSec = %d, want 2", got.SchedulerTickSec)
	}
	if got.EmailHost != "smtp.gmail.com" {
		t.Fatalf("EmailHost = %q, want smtp.gmail.com", got.EmailHost)
	}
	if got.EmailPort != 587 {
		t.Fatalf("EmailPort = %d, want 587", got.EmailPort)
	}
	if len(got.AllowedFixCommands) == 0 {
		t.Fatal("AllowedFixCommands is empty")
	}
	if runtime.GOOS == "windows" {
		wantCommands(t, got.AllowedFixCommands, []string{"cmd", "bash"})
	} else {
		wantCommands(t, got.AllowedFixCommands, []string{"bash"})
	}
	if len(got.AllowedFixTools) == 0 {
		t.Fatal("AllowedFixTools is empty")
	}
	// Verify key defaults are present.
	for _, want := range []string{"bash", "npm", "pm2", "git", "curl"} {
		found := false
		for _, tool := range got.AllowedFixTools {
			if tool == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("AllowedFixTools missing default %q", want)
		}
	}
}

func TestLoadReadsDotEnvWithoutOverridingProcessEnv(t *testing.T) {
	t.Chdir(t.TempDir())
	clearConfigEnv(t)
	t.Setenv("API_ADDR", ":9000")

	err := os.WriteFile(".env", []byte(`
API_ADDR=:7000
REDIS_DB=2
SCHEDULER_TICK_SEC=bad
ALLOWED_FIX_COMMANDS= bash, cmd.exe, , powershell 
ALLOWED_FIX_TOOLS=npm,git,docker
EMAIL_USER='alerts@example.com'
EMAIL_FROM=""
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	got := Load()

	if got.APIAddr != ":9000" {
		t.Fatalf("APIAddr = %q, want process env to win", got.APIAddr)
	}
	if got.RedisDB != 2 {
		t.Fatalf("RedisDB = %d, want value from .env", got.RedisDB)
	}
	if got.SchedulerTickSec != 2 {
		t.Fatalf("SchedulerTickSec = %d, want fallback for invalid int", got.SchedulerTickSec)
	}
	wantCommands(t, got.AllowedFixCommands, []string{"bash", "cmd.exe", "powershell"})
	wantCommands(t, got.AllowedFixTools, []string{"npm", "git", "docker"})
	if got.EmailUser != "alerts@example.com" {
		t.Fatalf("EmailUser = %q, want stripped quoted value", got.EmailUser)
	}
	if got.EmailFrom != got.EmailUser {
		t.Fatalf("EmailFrom = %q, want fallback to EmailUser", got.EmailFrom)
	}
}

func TestEnvCSVFallsBackWhenOnlyBlankItems(t *testing.T) {
	t.Setenv("TEST_CSV", " , ,, ")

	got := envCSV("TEST_CSV", []string{"fallback"})

	wantCommands(t, got, []string{"fallback"})
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"API_ADDR",
		"DATABASE_URL",
		"REDIS_ADDR",
		"REDIS_PASSWORD",
		"REDIS_DB",
		"SCHEDULER_TICK_SEC",
		"FIX_SCRIPTS_DIR",
		"ALLOWED_FIX_COMMANDS",
		"ALLOWED_FIX_TOOLS",
		"FIX_ENV_SECRET",
		"ALERT_COOLDOWN_SEC",
		"APP_ENV",
		"UI_DIR",
		"EMAIL_HOST",
		"EMAIL_PORT",
		"EMAIL_USER",
		"EMAIL_PASS",
		"EMAIL_FROM",
	} {
		t.Setenv(key, "")
	}
}

func wantCommands(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item %d = %q, want %q (got %#v)", i, got[i], want[i], got)
		}
	}
}
