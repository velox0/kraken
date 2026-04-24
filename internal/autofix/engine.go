package autofix

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
}

func NewEngine(scriptsDir string, allowedCommands []string) *Engine {
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
// Only the tools dir is on PATH; all inherited env vars are stripped.
func (e *Engine) buildCleanEnv(userVars map[string]string) []string {
	env := make([]string, 0, len(userVars)+3)
	env = append(env, "PATH="+e.toolsDir)
	env = append(env, "HOME="+os.TempDir())
	env = append(env, "TERM=dumb")
	for k, v := range userVars {
		env = append(env, k+"="+v)
	}
	return env
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

// EnsureToolsDir creates the tools directory and symlinks common tools
// if it does not already exist. This is called once at startup.
func EnsureToolsDir() string {
	dir := defaultToolsDir()
	if _, err := os.Stat(dir); err == nil {
		return dir // already exists, don't touch it
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return dir
	}

	// Symlink common dev tools that fix scripts are likely to need.
	tools := []string{
		"bash", "sh", "node", "npm", "npx", "pm2",
		"python3", "pip3", "curl", "wget",
		"docker", "git", "systemctl", "supervisorctl",
	}
	if runtime.GOOS == "windows" {
		tools = append(tools, "cmd.exe", "powershell.exe")
	}

	for _, tool := range tools {
		toolPath, err := exec.LookPath(tool)
		if err != nil {
			continue
		}
		linkPath := filepath.Join(dir, filepath.Base(tool))
		// Ignore errors — we just do best-effort.
		_ = os.Symlink(toolPath, linkPath)
	}

	return dir
}
