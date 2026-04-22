// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Canonical Ltd.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/canonical/snapd-wsl-tests/internal/wsl"
)

// importDistro imports a rootfs into WSL with retry logic.
// On failure, it unregisters the distro, removes the VM directory,
// restarts the WSL service, and retries up to 3 times.
func importDistro(ctx context.Context, distroName, rootfsPath, vmPath string) error {
	maxAttempts := 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			slog.Info("Retrying WSL import", "attempt", attempt, "max_attempts", maxAttempts)

			// Unregister existing distro (ignore "not found" errors)
			if err := wsl.RunCommand(ctx, "wsl.exe", "--unregister", distroName); err != nil {
				// Check if it's a "not found" error
				if ok := isDistroNotFoundError(err); !ok {
					slog.Warn("Failed to unregister distro", "err", err)
				}
			}

			// Remove VM directory
			if err := os.RemoveAll(vmPath); err != nil && !os.IsNotExist(err) {
				slog.Warn("Failed to remove VM directory", "path", vmPath, "err", err)
			}

			// Restart WSL service
			if err := restartWSLService(ctx); err != nil {
				slog.Warn("Failed to restart WSL service", "err", err)
			}

			slog.Info("Waiting before retry", "seconds", 10)
			// Context-aware wait: respect cancellation without forcing full wait
			timer := time.NewTimer(10 * time.Second)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return ctx.Err()
			case <-timer.C:
			}
		}

		slog.Info("Running WSL import", "distro", distroName, "attempt", attempt)
		err := wsl.RunCommand(ctx, "wsl.exe", "--import", distroName, vmPath, rootfsPath, "--version", "2")
		if err == nil {
			slog.Info("WSL import successful", "distro", distroName)
			return nil
		}

		lastErr = err
		slog.Warn("WSL import attempt failed", "attempt", attempt, "err", err)
		showWslImportDiagnostics(ctx, fmt.Sprintf("import attempt %d of %d failed", attempt, maxAttempts))
	}

	return fmt.Errorf("cannot import distro %s after %d attempts: %w", distroName, maxAttempts, lastErr)
}

// isDistroNotFoundError checks if an error is about a distro not being found
func isDistroNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "not found") ||
		strings.Contains(errMsg, "no distribution") ||
		strings.Contains(errMsg, "there is no distribution")
}

// restartWSLService restarts the WslService or LxssManager.
// Returns any non-nil error so callers can decide whether to treat it as fatal.
func restartWSLService(ctx context.Context) error {
	// Try to find and restart a WSL service
	// On Windows, use PowerShell to manage services
	err := wsl.RunCommand(ctx, "powershell.exe", "-NoProfile", "-Command",
		"Get-Service LxssManager, WslService -ErrorAction SilentlyContinue | Restart-Service -Force -ErrorAction SilentlyContinue")
	if err != nil {
		// Log the failure but let the caller decide whether it is ignorable
		slog.Debug("WSL service restart command exited with status", "err", err)
		return err
	}
	return nil
}

// parseWslConf parses a wsl.conf INI file and returns a map of sections with key-value pairs.
// It trims surrounding whitespace and ignores comments and blank lines,
// so original formatting and line endings are not preserved.
func parseWslConf(content []byte) map[string]map[string]string {
	sections := make(map[string]map[string]string)
	currentSection := ""

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		// Check for section header: [section]
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.TrimSpace(line[1 : len(line)-1])
			if _, exists := sections[currentSection]; !exists {
				sections[currentSection] = make(map[string]string)
			}
			continue
		}

		// Parse key=value
		if strings.Contains(line, "=") && currentSection != "" {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				sections[currentSection][key] = value
			}
		}
	}

	return sections
}

// writeWslConf writes the wsl.conf file via the \\wsl.localhost\DISTRO\etc\wsl.conf path.
// Uses UTF-8 encoding and LF line endings only (no CRLF).
// Sections and keys are sorted for deterministic output.
func writeWslConf(distroName string, sections map[string]map[string]string) error {
	// Build the content string with proper formatting and sorted keys
	var sb strings.Builder

	// Sort section names for deterministic output
	sectionNames := make([]string, 0, len(sections))
	for section := range sections {
		sectionNames = append(sectionNames, section)
	}
	sort.Strings(sectionNames)

	for _, section := range sectionNames {
		kvs := sections[section]
		sb.WriteString(fmt.Sprintf("[%s]\n", section))

		// Sort keys within each section for deterministic output
		keys := make([]string, 0, len(kvs))
		for k := range kvs {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			sb.WriteString(fmt.Sprintf("%s=%s\n", k, kvs[k]))
		}
		sb.WriteString("\n")
	}

	content := []byte(sb.String())

	// Write to wsl.localhost path (accessible from Windows)
	wslConfPath := fmt.Sprintf("\\\\wsl.localhost\\%s\\etc\\wsl.conf", distroName)
	slog.Info("Writing wsl.conf", "path", wslConfPath)

	// Use os.WriteFile which handles UTF-8 properly
	return os.WriteFile(wslConfPath, content, 0o644)
}

// enableSystemd enables systemd in the distro's .wsl.conf
func enableSystemd(ctx context.Context, distroName string) error {
	// Read existing .wsl.conf
	wslConfPath := fmt.Sprintf("\\\\wsl.localhost\\%s\\etc\\wsl.conf", distroName)
	var content []byte
	if _, err := os.Stat(wslConfPath); err == nil {
		var readErr error
		content, readErr = os.ReadFile(wslConfPath)
		if readErr != nil {
			slog.Warn("Failed to read existing wsl.conf", "path", wslConfPath, "err", readErr)
			content = []byte{}
		}
	}

	// Parse and update
	sections := parseWslConf(content)
	if sections["boot"] == nil {
		sections["boot"] = make(map[string]string)
	}
	sections["boot"]["systemd"] = "true"

	// Write back
	if err := writeWslConf(distroName, sections); err != nil {
		return fmt.Errorf("cannot enable systemd (write wsl.conf): %w", err)
	}

	slog.Info("Terminating distro for changes to take effect", "distro", distroName)
	if err := wsl.RunWSLCommand(ctx, "--terminate", distroName); err != nil {
		return fmt.Errorf("cannot enable systemd (terminate distro): %w", err)
	}

	// Probe systemd state
	slog.Info("Checking if systemd is running", "distro", distroName)
	cmd := exec.CommandContext(ctx, "wsl.exe",
		"--distribution", distroName,
		"--user", "root",
		"--cd", "/",
		"--exec", "/bin/sh", "-lc",
		`state="$(systemctl is-system-running --wait || true)"; printf "%s\n" "$state"; case "$state" in running|degraded) exit 0 ;; *) exit 1 ;; esac`,
	)
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")

	output, err := cmd.CombinedOutput()
	state := strings.TrimSpace(string(output))

	if state == "degraded" {
		slog.Warn("Systemd reached degraded state (expected for some distros)", "distro", distroName)
		// Show failed units for diagnostics
		wsl.RunDiagnostic(ctx, "Failed systemd units",
			"--distribution", distroName,
			"--user", "root",
			"--cd", "/",
			"--exec", "systemctl", "--failed", "--no-pager", "--full",
		)
		return nil
	}

	if err != nil {
		return fmt.Errorf("cannot enable systemd (probe failed with state: %s): %w", state, err)
	}

	slog.Info("Systemd probe successful", "state", state)
	return nil
}

// setupWSLInstance configures the distro with required groups, users, and sudo rules.
// Uses retry logic to handle transient failures.
func setupWSLInstance(ctx context.Context, distroName string) error {
	maxAttempts := 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			slog.Info("Retrying WSL instance setup", "attempt", attempt)
			// Terminate and wait before retry
			_ = wsl.RunCommand(ctx, "wsl.exe", "--terminate", distroName)

			// Context-aware wait: respect cancellation without forcing full wait
			timer := time.NewTimer(10 * time.Second)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return ctx.Err()
			case <-timer.C:
			}
		}

		// Create docker system group using shell to ensure PATH is set up
		slog.Info("Adding docker system group")
		if err := wsl.RunCommand(ctx,
			"wsl.exe",
			"--distribution", distroName,
			"--user", "root",
			"--cd", "/",
			"--exec", "/bin/sh", "-lc",
			"groupadd --force --system docker",
		); err != nil {
			slog.Warn("Failed to add docker group", "err", err)
			continue
		}

		// Create lxd system group using shell to ensure PATH is set up
		slog.Info("Adding lxd system group")
		if err := wsl.RunCommand(ctx,
			"wsl.exe",
			"--distribution", distroName,
			"--user", "root",
			"--cd", "/",
			"--exec", "/bin/sh", "-lc",
			"groupadd --force --system lxd",
		); err != nil {
			slog.Warn("Failed to add lxd group", "err", err)
			continue
		}

		// Create test user using shell to ensure PATH is set up
		slog.Info("Adding test user 'user'")
		if err := wsl.RunCommand(ctx,
			"wsl.exe",
			"--distribution", distroName,
			"--user", "root",
			"--cd", "/",
			"--exec", "/bin/sh", "-lc",
			`id user > /dev/null 2>&1 || useradd --create-home --skel /etc/skel --groups users,admin,lxd,docker --shell=/bin/bash user`,
		); err != nil {
			slog.Warn("Failed to add test user", "err", err)
			continue
		}

		// Add sudo rule from inside the distro so Linux ownership and mode are applied correctly
		slog.Info("Allowing test user to use sudo")
		if err := wsl.RunWSLCommand(ctx,
			"--distribution", distroName,
			"--user", "root",
			"--cd", "/",
			"--exec", "/bin/sh", "-lc",
			`tmp="$(mktemp)" && printf '%s\n' 'user ALL= NOPASSWD: ALL' > "$tmp" && install -o root -g root -m 0440 "$tmp" /etc/sudoers.d/user.conf && rm -f "$tmp"`,
		); err != nil {
			slog.Warn("Failed to create sudo rule", "err", err)
			continue
		}

		slog.Info("WSL instance setup successful")
		return nil
	}

	return fmt.Errorf("cannot set up WSL instance after %d attempts", maxAttempts)
}

// setupWorkspaceSymlink creates a symlink from /srv/github-workspace in WSL to the Windows workspace.
// Returns the symlink path in WSL.
func setupWorkspaceSymlink(ctx context.Context, distroName, windowsPath string) (string, error) {
	slog.Info("Converting Windows path to WSL path", "windows_path", windowsPath)

	// Convert Windows path to WSL path using wslpath
	cmd := exec.CommandContext(ctx, "wsl.exe",
		"--distribution", distroName,
		"--exec", "wslpath", "-u", windowsPath,
	)
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("cannot convert Windows path to WSL path: %w", err)
	}

	linuxPath := strings.TrimSpace(string(output))
	slog.Info("Converted path", "linux_path", linuxPath)

	// Create symlink in WSL
	// Create symlink in WSL. Remove any existing path first so reruns are idempotent.
	slog.Info("Creating workspace symlink", "symlink", "/srv/github-workspace", "target", linuxPath)
	if err := wsl.RunWSLCommand(ctx,
		"--distribution", distroName,
		"--user", "root",
		"--cd", "/",
		"--exec", "/bin/sh", "-c", "rm -rf /srv/github-workspace && ln -s \"$1\" /srv/github-workspace", "sh", linuxPath,
	); err != nil {
		return "", fmt.Errorf("cannot create workspace symlink: %w", err)
	}

	// Verify symlink was created
	wsl.RunDiagnostic(ctx, "Workspace symlink info",
		"--distribution", distroName,
		"--user", "root",
		"--cd", "/",
		"--exec", "ls", "-ld", "/srv/github-workspace",
	)

	return "/srv/github-workspace", nil
}
