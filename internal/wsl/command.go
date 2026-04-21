// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Canonical Ltd.

// Package wsl provides utilities for WSL command execution.
package wsl

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
)

// RunCommand runs a generic command with the given arguments.
// Command output goes to stdout/stderr.
func RunCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunWSLCommand runs a wsl.exe command with WSL_UTF8=1 to ensure UTF-8 output.
// Command output goes to stdout/stderr.
func RunWSLCommand(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "wsl.exe", args...)
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunDiagnostic runs a wsl.exe command for diagnostic purposes and never fails
// the step — some commands return non-zero when there are no distributions.
func RunDiagnostic(ctx context.Context, label string, args ...string) {
	slog.Info(label)
	if err := RunWSLCommand(ctx, args...); err != nil {
		slog.Warn("Diagnostic command failed (expected in some states)", "err", err)
	}
}
