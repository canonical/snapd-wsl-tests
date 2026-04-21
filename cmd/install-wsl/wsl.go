// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Canonical Ltd.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// goArchToWSLArch converts the Go runtime architecture to the WSL asset suffix.
func goArchToWSLArch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "x64"
}

// getInstalledWSLVersion runs "wsl.exe --version" and parses the version line.
// Returns an empty string (and nil error) when WSL is not installed or the
// version line cannot be found.
func getInstalledWSLVersion(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "wsl.exe", "--version")
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		// wsl.exe absent or returned non-zero — treat as not installed.
		return "", nil
	}

	// Strip UTF-8 BOM and null bytes (the latter can appear in UTF-16LE output
	// from older WSL builds that ignore WSL_UTF8).
	s := strings.TrimPrefix(string(out), "\xef\xbb\xbf")
	s = strings.ReplaceAll(s, "\x00", "")
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		const prefix = "WSL version:"
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix)), nil
		}
	}

	return "", nil
}

func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runWSLCommand runs a wsl.exe command with WSL_UTF8=1 to ensure UTF-8 output.
func runWSLCommand(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "wsl.exe", args...)
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runDiagnostic runs a wsl.exe command for diagnostic purposes and never fails
// the step — some commands return non-zero when there are no distributions.
func runDiagnostic(ctx context.Context, label string, args ...string) {
	slog.Info(label)
	if err := runWSLCommand(ctx, args...); err != nil {
		slog.Warn("Diagnostic command failed (expected in some states)", "err", err)
	}
}

func runInstallMSI(ctx context.Context, msiPath string) error {
	err := runCommand(ctx, "msiexec.exe", "/quiet", "/passive", "/package", msiPath)
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		switch exitErr.ExitCode() {
		case 1641, 3010:
			// Restart queued or required. WSL should still be usable.
			slog.Info("Msiexec completed with restart pending", "exit_code", exitErr.ExitCode())
			return nil
		}
	}
	return fmt.Errorf("cannot run msiexec (install package %s): %w", msiPath, err)
}

type wslConfig struct {
	VMIdleTimeout     string `ini:"vmIdleTimeout,omitempty"`
	Kernel            string `ini:"kernel,omitempty"`
	KernelCommandLine string `ini:"kernelCommandLine,omitempty"`
}

func writeWslConfig(homeDir string, cfg wslConfig) error {
	if homeDir == "" {
		return fmt.Errorf("cannot write .wslconfig: home directory is empty")
	}

	var bb bytes.Buffer

	bb.WriteString("[wsl2]\n")
	if cfg.VMIdleTimeout != "" {
		fmt.Fprintf(&bb, "vmIdleTimeout = %s\n", cfg.VMIdleTimeout)
	}

	if cfg.Kernel != "" {
		// .wslconfig uses INI-like syntax; backslashes must be doubled.
		fmt.Fprintf(&bb, "kernel = %s\n", strings.ReplaceAll(cfg.Kernel, `\`, `\\`))
	}

	if cfg.KernelCommandLine != "" {
		fmt.Fprintf(&bb, "kernelCommandLine = %s\n", cfg.KernelCommandLine)
	}

	configPath := filepath.Join(homeDir, ".wslconfig")

	slog.Info("Writing wslconfig", "path", configPath)

	return os.WriteFile(configPath, bb.Bytes(), 0o644)
}
