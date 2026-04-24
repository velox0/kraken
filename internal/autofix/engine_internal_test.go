package autofix

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEngineResolvePathConstrainedToScriptsDir(t *testing.T) {
	t.Parallel()

	base := filepath.Join(t.TempDir(), "scripts")
	e := NewEngine(base, []string{"bash"})

	got, err := e.resolvePath("restart.sh")
	if err != nil {
		t.Fatalf("resolvePath returned error: %v", err)
	}
	if got != filepath.Join(base, "restart.sh") {
		t.Fatalf("resolvePath = %q, want joined path", got)
	}

	insideDotDotName := filepath.Join(base, "..not-parent", "fix.sh")
	got, err = e.resolvePath(insideDotDotName)
	if err != nil {
		t.Fatalf("resolvePath rejected valid in-tree path: %v", err)
	}
	if got != filepath.Clean(insideDotDotName) {
		t.Fatalf("resolvePath = %q, want cleaned valid path", got)
	}

	if _, err := e.resolvePath("../escape.sh"); err == nil {
		t.Fatal("resolvePath allowed relative escape")
	}
	if _, err := e.resolvePath(filepath.Join(filepath.Dir(base), "escape.sh")); err == nil {
		t.Fatal("resolvePath allowed absolute escape")
	}
	if _, err := e.resolvePath(" "); err == nil {
		t.Fatal("resolvePath allowed blank path")
	}
}

func TestEngineBuildCleanEnvDoesNotInheritProcessEnv(t *testing.T) {
	t.Parallel()

	e := NewEngine("/tmp/scripts", []string{"bash"})
	e.toolsDir = "/kraken/tools"

	got := e.buildCleanEnv(map[string]string{"TOKEN": "secret", "EMPTY": ""})
	joined := "\n" + strings.Join(got, "\n") + "\n"

	for _, want := range []string{"\nPATH=/kraken/tools\n", "\nHOME=" + os.TempDir() + "\n", "\nTERM=dumb\n", "\nTOKEN=secret\n", "\nEMPTY=\n"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("env missing %q in %#v", want, got)
		}
	}
	if strings.Contains(joined, "\nDATABASE_URL=") {
		t.Fatalf("env unexpectedly inherited DATABASE_URL: %#v", got)
	}
}

func TestEngineExecuteAllowlistAndOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script execution behavior is covered by resolveRunner on windows")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "ok.sh")
	if err := os.WriteFile(script, []byte("printf 'token=%s\\n' \"$TOKEN\"\nprintf 'stderr-line\\n' >&2\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	e := NewEngine(dir, []string{" bash "})
	got, err := e.Execute(context.Background(), FixDefinition{
		ScriptPath: "ok.sh",
		TimeoutSec: 1,
		EnvVars: map[string]string{
			"TOKEN": "abc123",
		},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v, output: %q", err, got.Output)
	}
	if !got.Success {
		t.Fatal("Execute returned Success=false")
	}
	if !strings.Contains(got.Output, "token=abc123") || !strings.Contains(got.Output, "stderr-line") {
		t.Fatalf("output = %q, want stdout and stderr", got.Output)
	}
}

func TestEngineExecuteRejectsDisallowedRunner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-windows bash allowlist behavior only")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.sh"), []byte("printf ok\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	e := NewEngine(dir, []string{"cmd"})
	_, err := e.Execute(context.Background(), FixDefinition{ScriptPath: "ok.sh", TimeoutSec: 1})
	if err == nil {
		t.Fatal("expected allowlist error")
	}
	if !strings.Contains(err.Error(), "bash command is not in allowlist") {
		t.Fatalf("error = %q, want allowlist message", err.Error())
	}
}
