// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Canonical Ltd.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/canonical/snapd-wsl-tests/internal/wsl"
)

// showWslImportDiagnostics collects and displays diagnostic information for WSL import failures.
func showWslImportDiagnostics(ctx context.Context, reason string) {
	slog.Info("Collecting WSL import diagnostics", "reason", reason)
	fmt.Println("::group::WSL import diagnostics (" + reason + ")")
	defer fmt.Println("::endgroup::")

	fmt.Printf("Timestamp: %s\n", time.Now().Format(time.RFC3339))
	fmt.Printf("Reason: %s\n\n", reason)

	// Show WSL version and status
	wsl.RunDiagnostic(ctx, "WSL version",
		"--version",
	)

	wsl.RunDiagnostic(ctx, "WSL status",
		"--status",
	)

	wsl.RunDiagnostic(ctx, "WSL distributions",
		"--list", "--verbose",
	)

	// Show service status if Windows (best effort)
	showWindowsServiceStatus(ctx)
}

// showWindowsServiceStatus displays WSL-related Windows service status
func showWindowsServiceStatus(ctx context.Context) {
	fmt.Println("Windows service status:")

	// Try to get WslService status
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Command",
		"Get-Service -Name WslService, LxssManager -ErrorAction SilentlyContinue | Select-Object Name, Status | Format-Table -AutoSize",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run() // Ignore errors, this is diagnostic
}
