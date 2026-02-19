package builtin

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// code.run — execute a code snippet in a subprocess and return stdout/stderr.
//
// SAFETY: Must set ACL_CODE_RUN_ENABLED=1 to enable. Runs in a subprocess
// with a configurable timeout. No network isolation — use with trusted code only.
//
// Supported languages: python, javascript (node), bash, ruby
//
// Args:
//
//	language  string   python | javascript | bash | ruby  (required)
//	code      string   source code to run                 (required)
//	timeout   float    seconds, max 60 (default 10)
//	env       map      extra environment variables
//
// Returns: {stdout, stderr, exit_code, duration_ms, language}
func codeRun(ctx context.Context, args map[string]any) (any, error) {
	if os.Getenv("ACL_CODE_RUN_ENABLED") != "1" {
		return nil, fmt.Errorf("code.run: disabled — set ACL_CODE_RUN_ENABLED=1 to enable")
	}

	language, _ := args["language"].(string)
	code, _ := args["code"].(string)
	if language == "" {
		return nil, fmt.Errorf("code.run: language is required")
	}
	if code == "" {
		return nil, fmt.Errorf("code.run: code is required")
	}

	timeoutSec := 10.0
	if t, ok := args["timeout"].(float64); ok && t > 0 && t <= 60 {
		timeoutSec = t
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	switch strings.ToLower(language) {
	case "python", "python3":
		cmd = exec.CommandContext(ctx, "python3", "-c", code)
	case "javascript", "js", "node":
		cmd = exec.CommandContext(ctx, "node", "-e", code)
	case "bash", "sh":
		cmd = exec.CommandContext(ctx, "bash", "-c", code)
	case "ruby":
		cmd = exec.CommandContext(ctx, "ruby", "-e", code)
	default:
		return nil, fmt.Errorf("code.run: unsupported language %q (supported: python, javascript, bash, ruby)", language)
	}

	// Inject extra env vars.
	cmd.Env = os.Environ()
	if extra, ok := args["env"].(map[string]any); ok {
		for k, v := range extra {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%v", k, v))
		}
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start).Milliseconds()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// Context deadline or binary not found.
			return map[string]any{
				"stdout":      stdout.String(),
				"stderr":      err.Error(),
				"exit_code":   -1,
				"duration_ms": duration,
				"language":    language,
			}, nil
		}
	}

	return map[string]any{
		"stdout":      stdout.String(),
		"stderr":      stderr.String(),
		"exit_code":   exitCode,
		"duration_ms": duration,
		"language":    language,
	}, nil
}
