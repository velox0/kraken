package autofix

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type FixDefinition struct {
	Name       string
	ScriptPath string
	TimeoutSec int
	EnvVars    map[string]string
}

type Engine struct {
	scriptsDir       string
	toolsDir         string
	allowedCmdLookup map[string]struct{}

	mu           sync.RWMutex
	allowedTools []string
}

// NewEngine creates an autofix engine.
//   - scriptsDir: directory where fix scripts live.
//   - allowedCommands: runner commands allowed to execute scripts (e.g. "bash", "cmd").
//   - allowedTools: binaries that scripts may call (e.g. "pm2", "npm", "git").
//     Only these tools are symlinked into the sandboxed tools directory.
func NewEngine(scriptsDir string, allowedCommands []string, allowedTools []string) *Engine {
	lookup := make(map[string]struct{}, len(allowedCommands))
	for _, cmd := range allowedCommands {
		normalized := normalizeAllowedCommand(cmd)
		if normalized == "" {
			continue
		}
		lookup[normalized] = struct{}{}
	}
	return &Engine{
		scriptsDir:       scriptsDir,
		toolsDir:         defaultToolsDir(),
		allowedCmdLookup: lookup,
		allowedTools:     dedupTools(allowedTools),
	}
}

type Result struct {
	Success bool
	Output  string
}

func (e *Engine) Execute(ctx context.Context, fix FixDefinition) (Result, error) {
	timeoutSec := fix.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	fixCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	resolvedPath, err := e.resolvePath(fix.ScriptPath)
	if err != nil {
		return Result{}, err
	}

	runner, err := resolveRunner(runtime.GOOS, resolvedPath)
	if err != nil {
		return Result{}, err
	}
	if _, ok := e.allowedCmdLookup[runner.allowlistKey]; !ok {
		return Result{}, fmt.Errorf("%s command is not in allowlist", runner.allowlistKey)
	}

	// Resolve runner command from tools dir so we never use a system binary.
	runnerCmd := e.resolveToolPath(runner.command)

	args := append([]string{}, runner.args...)
	args = append(args, resolvedPath)
	cmd := exec.CommandContext(fixCtx, runnerCmd, args...)

	// Build a clean environment — no inherited env vars leak in.
	cmd.Env = e.buildCleanEnv(fix.EnvVars)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	output := strings.TrimSpace(stdout.String() + "\n" + stderr.String())
	if err != nil {
		if output == "" {
			output = err.Error()
		}
		return Result{Success: false, Output: truncate(output, 3000)}, err
	}
	return Result{Success: true, Output: truncate(output, 3000)}, nil
}

// buildCleanEnv builds a minimal environment for script execution.
// PATH is always set to the tools dir (security sandbox).
// HOME and TERM have sensible defaults but can be overridden by
// per-project env vars passed via userVars.
func (e *Engine) buildCleanEnv(userVars map[string]string) []string {
	// Defaults — overridable by project env vars.
	defaults := map[string]string{
		"HOME": userHomeDir(),
		"TERM": "dumb",
	}

	// User vars override defaults.
	merged := make(map[string]string, len(defaults)+len(userVars))
	for k, v := range defaults {
		merged[k] = v
	}
	for k, v := range userVars {
		merged[k] = v
	}

	// PATH is always the tools dir — not overridable.
	env := make([]string, 0, len(merged)+1)
	env = append(env, "PATH="+e.toolsDir)
	for k, v := range merged {
		if k == "PATH" {
			continue // PATH is sandboxed, never overridable
		}
		env = append(env, k+"="+v)
	}
	return env
}

// userHomeDir returns the current user's home directory.
// Falls back to os.TempDir() if the home directory cannot be determined.
func userHomeDir() string {
	if runtime.GOOS == "windows" {
		if h := os.Getenv("USERPROFILE"); h != "" {
			return h
		}
		if h := os.Getenv("HOMEDRIVE") + os.Getenv("HOMEPATH"); h != "" {
			return h
		}
	}
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.TempDir()
}

// DefaultFixEnvVars returns the default environment variables that should be
// seeded for every new project. These are the engine's internal defaults
// made visible and configurable per-project.
func DefaultFixEnvVars() []struct {
	Name     string
	Value    string
	IsSecret bool
} {
	return []struct {
		Name     string
		Value    string
		IsSecret bool
	}{
		{Name: "HOME", Value: userHomeDir(), IsSecret: true},
		{Name: "TERM", Value: "dumb", IsSecret: false},
	}
}

// resolveToolPath returns the full path to a tool in the tools directory.
// If the tools dir doesn't exist or the tool isn't found there, it falls
// back to the bare command name (system PATH resolution via exec.LookPath).
func (e *Engine) resolveToolPath(command string) string {
	if e.toolsDir == "" {
		return command
	}
	candidate := filepath.Join(e.toolsDir, command)
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	// On Windows, also check with .exe extension.
	if runtime.GOOS == "windows" {
		candidateExe := candidate + ".exe"
		if info, err := os.Stat(candidateExe); err == nil && !info.IsDir() {
			return candidateExe
		}
	}
	return command
}

func (e *Engine) resolvePath(scriptPath string) (string, error) {
	if strings.TrimSpace(scriptPath) == "" {
		return "", fmt.Errorf("script path is empty")
	}
	path := scriptPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(e.scriptsDir, scriptPath)
	}
	cleaned := filepath.Clean(path)
	base := filepath.Clean(e.scriptsDir)

	// Keep scripts constrained to the configured scripts directory.
	rel, err := filepath.Rel(base, cleaned)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("script path escapes scripts directory")
	}
	return cleaned, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

type runnerSpec struct {
	allowlistKey string
	command      string
	args         []string
}

func resolveRunner(goos, scriptPath string) (runnerSpec, error) {
	ext := strings.ToLower(filepath.Ext(scriptPath))
	switch goos {
	case "windows":
		switch ext {
		case ".bat", ".cmd":
			return runnerSpec{
				allowlistKey: "cmd",
				command:      "cmd.exe",
				args:         []string{"/C"},
			}, nil
		case ".sh", "":
			return runnerSpec{
				allowlistKey: "bash",
				command:      "bash",
			}, nil
		default:
			return runnerSpec{}, fmt.Errorf("unsupported script extension %q on windows (supported: .bat, .cmd, .sh)", ext)
		}
	default:
		switch ext {
		case ".bat", ".cmd":
			return runnerSpec{}, fmt.Errorf("%s scripts can only run on windows workers", ext)
		default:
			return runnerSpec{
				allowlistKey: "bash",
				command:      "bash",
			}, nil
		}
	}
}

func normalizeAllowedCommand(cmd string) string {
	c := strings.ToLower(strings.TrimSpace(cmd))
	c = strings.TrimSuffix(c, ".exe")
	return c
}

// defaultToolsDir returns the platform-appropriate tools directory.
func defaultToolsDir() string {
	if runtime.GOOS == "windows" {
		home := os.Getenv("USERPROFILE")
		if home == "" {
			home = os.Getenv("HOMEDRIVE") + os.Getenv("HOMEPATH")
		}
		if home != "" {
			return filepath.Join(home, ".krakentools")
		}
		return `C:\.krakentools`
	}
	home := os.Getenv("HOME")
	if home != "" {
		return filepath.Join(home, ".krakentools")
	}
	return "/root/.krakentools"
}

// AllowedTools returns the current tools whitelist.
func (e *Engine) AllowedTools() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	cp := make([]string, len(e.allowedTools))
	copy(cp, e.allowedTools)
	return cp
}

// SetAllowedTools replaces the tools whitelist and re-syncs the tools directory.
// Returns the result of the sync (which tools were linked/removed/failed).
func (e *Engine) SetAllowedTools(tools []string) SyncResult {
	deduped := dedupTools(tools)
	e.mu.Lock()
	e.allowedTools = deduped
	e.mu.Unlock()
	return e.SyncToolsDir()
}

// ToolStatus describes the resolution state of a single whitelisted tool.
type ToolStatus struct {
	Name       string `json:"name"`
	Resolved   bool   `json:"resolved"`
	SystemPath string `json:"system_path,omitempty"`
	LinkedPath string `json:"linked_path,omitempty"`
	Error      string `json:"error,omitempty"`
}

// SyncResult summarises what SyncToolsDir did.
type SyncResult struct {
	ToolsDir string       `json:"tools_dir"`
	Linked   []ToolStatus `json:"linked"`
	Removed  []string     `json:"removed"`
}

// SyncToolsDir rebuilds the tools directory to match the current whitelist.
// Tools not in the whitelist are removed; whitelisted tools are resolved via
// exec.LookPath and symlinked (or hardlinked/copied on Windows).
func (e *Engine) SyncToolsDir() SyncResult {
	e.mu.RLock()
	tools := make([]string, len(e.allowedTools))
	copy(tools, e.allowedTools)
	e.mu.RUnlock()

	dir := e.toolsDir
	result := SyncResult{ToolsDir: dir}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return result
	}

	// Build a set of desired tool basenames.
	wanted := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		wanted[t] = struct{}{}
	}

	// Remove entries that are no longer whitelisted.
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		name := entry.Name()
		base := name
		// On Windows strip .exe for comparison.
		if runtime.GOOS == "windows" {
			base = strings.TrimSuffix(strings.ToLower(name), ".exe")
		}
		if _, ok := wanted[base]; !ok {
			linkPath := filepath.Join(dir, name)
			if err := os.Remove(linkPath); err == nil {
				result.Removed = append(result.Removed, name)
			}
		}
	}

	// Link each whitelisted tool.
	for _, tool := range tools {
		ts := ToolStatus{Name: tool}
		toolPath, err := exec.LookPath(tool)
		if err != nil {
			ts.Error = fmt.Sprintf("not found on system: %v", err)
			result.Linked = append(result.Linked, ts)
			continue
		}
		ts.SystemPath = toolPath

		linkPath := filepath.Join(dir, filepath.Base(tool))
		ts.LinkedPath = linkPath

		// Remove existing entry (may be stale).
		_ = os.Remove(linkPath)

		if err := linkTool(toolPath, linkPath); err != nil {
			ts.Error = err.Error()
		} else {
			ts.Resolved = true
		}
		result.Linked = append(result.Linked, ts)
	}

	return result
}

// linkTool creates a symlink from systemPath to linkPath.
// On Windows, where symlinks often require elevated privileges, it falls
// back to a hard link and then a file copy.
func linkTool(systemPath, linkPath string) error {
	err := os.Symlink(systemPath, linkPath)
	if err == nil {
		return nil
	}

	if runtime.GOOS != "windows" {
		return fmt.Errorf("symlink failed: %w", err)
	}

	// Windows fallback: try hard link.
	if hlErr := os.Link(systemPath, linkPath); hlErr == nil {
		return nil
	}

	// Last resort: copy the file.
	return copyFile(systemPath, linkPath)
}

// copyFile performs a best-effort file copy for Windows tool resolution.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("copy open src: %w", err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o750)
	if err != nil {
		return fmt.Errorf("copy create dst: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy data: %w", err)
	}
	return nil
}

// dedupTools returns a de-duplicated, trimmed copy of the tools list.
func dedupTools(tools []string) []string {
	seen := make(map[string]struct{}, len(tools))
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}
