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
	e := NewEngine(base, []string{"bash"}, nil)

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

	e := NewEngine("/tmp/scripts", []string{"bash"}, nil)
	e.toolsDir = "/kraken/tools"

	got := e.buildCleanEnv(map[string]string{"TOKEN": "secret", "EMPTY": ""})
	joined := "\n" + strings.Join(got, "\n") + "\n"

	// PATH must be the tools dir (sandboxed).
	if !strings.Contains(joined, "\nPATH=/kraken/tools\n") {
		t.Fatalf("env missing sandboxed PATH in %#v", got)
	}
	// HOME defaults to real home.
	if !strings.Contains(joined, "\nHOME="+os.Getenv("HOME")+"\n") {
		t.Fatalf("env missing HOME in %#v", got)
	}
	// TERM defaults to dumb.
	if !strings.Contains(joined, "\nTERM=dumb\n") {
		t.Fatalf("env missing TERM in %#v", got)
	}
	// User vars are present.
	if !strings.Contains(joined, "\nTOKEN=secret\n") || !strings.Contains(joined, "\nEMPTY=\n") {
		t.Fatalf("env missing user vars in %#v", got)
	}
	// System env vars are stripped.
	if strings.Contains(joined, "\nDATABASE_URL=") {
		t.Fatalf("env unexpectedly inherited DATABASE_URL: %#v", got)
	}
}

func TestEngineBuildCleanEnvUserVarsOverrideDefaults(t *testing.T) {
	t.Parallel()

	e := NewEngine("/tmp/scripts", []string{"bash"}, nil)
	e.toolsDir = "/kraken/tools"

	// User var overrides the HOME default.
	got := e.buildCleanEnv(map[string]string{"HOME": "/custom/home", "TERM": "xterm-256color"})
	joined := "\n" + strings.Join(got, "\n") + "\n"

	if !strings.Contains(joined, "\nHOME=/custom/home\n") {
		t.Fatalf("user HOME override not applied: %#v", got)
	}
	if !strings.Contains(joined, "\nTERM=xterm-256color\n") {
		t.Fatalf("user TERM override not applied: %#v", got)
	}
	// PATH should still be sandboxed even if user tries to override.
	if !strings.Contains(joined, "\nPATH=/kraken/tools\n") {
		t.Fatalf("PATH was overridden: %#v", got)
	}
}

func TestEngineBuildCleanEnvPathNotOverridable(t *testing.T) {
	t.Parallel()

	e := NewEngine("/tmp/scripts", []string{"bash"}, nil)
	e.toolsDir = "/kraken/tools"

	// Attempt to override PATH via user vars — should be ignored.
	got := e.buildCleanEnv(map[string]string{"PATH": "/malicious/bin"})
	joined := "\n" + strings.Join(got, "\n") + "\n"

	if strings.Contains(joined, "/malicious/bin") {
		t.Fatalf("PATH was overridable: %#v", got)
	}
	if !strings.Contains(joined, "\nPATH=/kraken/tools\n") {
		t.Fatalf("sandboxed PATH missing: %#v", got)
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

	e := NewEngine(dir, []string{" bash "}, nil)
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

	e := NewEngine(dir, []string{"cmd"}, nil)
	_, err := e.Execute(context.Background(), FixDefinition{ScriptPath: "ok.sh", TimeoutSec: 1})
	if err == nil {
		t.Fatal("expected allowlist error")
	}
	if !strings.Contains(err.Error(), "bash command is not in allowlist") {
		t.Fatalf("error = %q, want allowlist message", err.Error())
	}
}

func TestSyncToolsDirLinksAndRemoves(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test not reliable on Windows CI")
	}

	toolsDir := filepath.Join(t.TempDir(), "tools")
	e := NewEngine(t.TempDir(), []string{"bash"}, []string{"bash", "sh"})
	e.toolsDir = toolsDir

	result := e.SyncToolsDir()
	if result.ToolsDir != toolsDir {
		t.Fatalf("ToolsDir = %q, want %q", result.ToolsDir, toolsDir)
	}

	// bash and sh should be in the linked list.
	foundBash := false
	for _, ts := range result.Linked {
		if ts.Name == "bash" {
			foundBash = true
			if !ts.Resolved {
				t.Fatalf("bash not resolved: %s", ts.Error)
			}
		}
	}
	if !foundBash {
		t.Fatal("bash not found in linked list")
	}

	// The tools dir should have the symlinks.
	bashLink := filepath.Join(toolsDir, "bash")
	if _, err := os.Lstat(bashLink); err != nil {
		t.Fatalf("bash symlink not found: %v", err)
	}

	// Now remove bash from whitelist.
	result2 := e.SetAllowedTools([]string{"sh"})
	foundRemoved := false
	for _, r := range result2.Removed {
		if r == "bash" {
			foundRemoved = true
		}
	}
	if !foundRemoved {
		t.Fatal("bash was not removed when dropped from whitelist")
	}
	if _, err := os.Lstat(bashLink); !os.IsNotExist(err) {
		t.Fatal("bash symlink still exists after removal")
	}
}

func TestDedupTools(t *testing.T) {
	t.Parallel()

	got := dedupTools([]string{"npm", "  npm ", "git", "", "git", "curl"})
	want := []string{"npm", "git", "curl"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAllowedToolsThreadSafety(t *testing.T) {
	t.Parallel()

	e := NewEngine(t.TempDir(), []string{"bash"}, []string{"npm", "git"})

	tools := e.AllowedTools()
	if len(tools) != 2 {
		t.Fatalf("AllowedTools len = %d, want 2", len(tools))
	}

	// Mutating the returned slice should not affect the engine.
	tools[0] = "MUTATED"
	fresh := e.AllowedTools()
	if fresh[0] == "MUTATED" {
		t.Fatal("AllowedTools returned a reference, not a copy")
	}
}
