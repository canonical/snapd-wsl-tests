// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Canonical Ltd.

// import-wsl-distro imports a WSL rootfs image and configures it.
// It is invoked by the setup-distro GitHub Actions composite action via "go run ./cmd/import-wsl-distro".
//
// All configuration is read from environment variables:
//
//	WSL_DISTRO_NAME           required; name of WSL distro instance
//	WSL_ROOTFS_URL            required; URL to download rootfs from
//	WSL_ROOTFS_FILE           required; local filename for rootfs
//	WSL_ROOTFSES_DIR          optional; directory for rootfses (default "wsl-rootfses")
//	WSL_VMS_DIR               optional; directory for VM disks (default "wsl-vms")
//	WSL_ENABLE_SYSTEMD        optional; enable systemd boot (default "false")
//	GITHUB_WORKSPACE          required; GitHub workspace path on Windows
//	GITHUB_OUTPUT             required; GitHub output file path
//
// Output is written to $GITHUB_OUTPUT: wsl-workspace-path=/srv/github-workspace
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/canonical/snapd-wsl-tests/internal/dl"
)

func run(ctx context.Context) error {
	// Load all environment variables early
	distroName := os.Getenv("WSL_DISTRO_NAME")
	if distroName == "" {
		return fmt.Errorf("cannot read WSL_DISTRO_NAME environment variable (required)")
	}

	rootfsURL := os.Getenv("WSL_ROOTFS_URL")
	if rootfsURL == "" {
		return fmt.Errorf("cannot read WSL_ROOTFS_URL environment variable (required)")
	}

	rootfsFile := os.Getenv("WSL_ROOTFS_FILE")
	if rootfsFile == "" {
		return fmt.Errorf("cannot read WSL_ROOTFS_FILE environment variable (required)")
	}

	// Validate distroName: must be alphanumeric + hyphens/underscores/dots, no path traversal
	if err := validateDistroName(distroName); err != nil {
		return err
	}

	// Validate rootfsFile: must be a base filename with no path separators or traversal
	if err := validateBaseFilename(rootfsFile); err != nil {
		return err
	}

	rootfsesDir := os.Getenv("WSL_ROOTFSES_DIR")
	if rootfsesDir == "" {
		rootfsesDir = "wsl-rootfses"
	}

	vmsDir := os.Getenv("WSL_VMS_DIR")
	if vmsDir == "" {
		vmsDir = "wsl-vms"
	}

	enableSystemdStr := os.Getenv("WSL_ENABLE_SYSTEMD")
	enableSystemdFlag := false
	if enableSystemdStr != "" {
		var err error
		enableSystemdFlag, err = strconv.ParseBool(enableSystemdStr)
		if err != nil {
			return fmt.Errorf("cannot parse WSL_ENABLE_SYSTEMD (must be 'true' or 'false'): %w", err)
		}
	}

	githubWorkspace := os.Getenv("GITHUB_WORKSPACE")
	if githubWorkspace == "" {
		return fmt.Errorf("cannot read GITHUB_WORKSPACE environment variable (required)")
	}

	githubOutput := os.Getenv("GITHUB_OUTPUT")
	if githubOutput == "" {
		return fmt.Errorf("cannot read GITHUB_OUTPUT environment variable (required)")
	}

	slog.Info("Importing WSL distro", "distro", distroName, "rootfs_file", rootfsFile)

	// Prepare directories
	if err := os.MkdirAll(rootfsesDir, 0o755); err != nil {
		return fmt.Errorf("cannot create rootfses directory: %w", err)
	}

	if err := os.MkdirAll(vmsDir, 0o755); err != nil {
		return fmt.Errorf("cannot create VMs directory: %w", err)
	}

	rootfsPath := filepath.Join(rootfsesDir, rootfsFile)
	vmPath := filepath.Join(vmsDir, distroName)

	// Download rootfs unless it was already restored from cache
	if _, err := os.Stat(rootfsPath); err == nil {
		slog.Info("Using cached rootfs", "path", rootfsPath)
	} else if os.IsNotExist(err) {
		preflightCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()

		if err := dl.CheckURLReachable(preflightCtx, rootfsURL); err != nil {
			suggestion := suggestUbuntuWSLURL(rootfsURL, rootfsFile)
			if suggestion != "" {
				return fmt.Errorf("cannot verify rootfs URL %q before download: %w; try %q", rootfsURL, err, suggestion)
			}
			return fmt.Errorf("cannot verify rootfs URL %q before download: %w", rootfsURL, err)
		}

		slog.Info("Downloading rootfs")
		if err := dl.DownloadFile(ctx, rootfsURL, rootfsPath); err != nil {
			return fmt.Errorf("cannot download rootfs: %w", err)
		}
	} else {
		return fmt.Errorf("cannot stat rootfs path %q: %w", rootfsPath, err)
	}

	// Import rootfs into WSL
	slog.Info("Importing rootfs into WSL")
	if err := importDistro(ctx, distroName, rootfsPath, vmPath); err != nil {
		return err
	}

	// Setup WSL instance (groups, users, sudo)
	slog.Info("Setting up WSL instance")
	if err := setupWSLInstance(ctx, distroName); err != nil {
		return err
	}

	// Enable systemd if requested
	if enableSystemdFlag {
		slog.Info("Enabling systemd")
		if err := enableSystemd(ctx, distroName); err != nil {
			return err
		}
	}

	// Setup workspace symlink
	slog.Info("Setting up workspace symlink")
	workspacePath, err := setupWorkspaceSymlink(ctx, distroName, githubWorkspace)
	if err != nil {
		return err
	}

	// Write output
	slog.Info("Writing output")
	if err := writeOutput(githubOutput, workspacePath); err != nil {
		return fmt.Errorf("cannot write output: %w", err)
	}

	slog.Info("WSL distro import complete", "workspace_path", workspacePath)
	return nil
}

func writeOutput(outputFile, workspacePath string) error {
	f, err := os.OpenFile(outputFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("cannot open output file: %w", err)
	}
	defer f.Close()

	_, err = fmt.Fprintf(f, "wsl-workspace-path=%s\n", workspacePath)
	return err
}

// validateDistroName ensures the distro name is safe for use in paths and UNC paths.
// Must be alphanumeric plus hyphens, underscores, and dots; no path separators, ".", or ".." sequences.
func validateDistroName(name string) error {
	if name == "" {
		return fmt.Errorf("cannot validate WSL_DISTRO_NAME: name cannot be empty")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("cannot validate WSL_DISTRO_NAME: name cannot be '.' or '..'")
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("cannot validate WSL_DISTRO_NAME: name contains '..' path traversal sequence")
	}
	if filepath.IsAbs(name) {
		return fmt.Errorf("cannot validate WSL_DISTRO_NAME: name cannot be an absolute path")
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
			return fmt.Errorf("cannot validate WSL_DISTRO_NAME: name contains invalid character %q (only alphanumeric, hyphens, underscores, and dots allowed)", c)
		}
	}
	return nil
}

// validateBaseFilename ensures the filename is a base name with no directory separators, ".", or "..".
func validateBaseFilename(filename string) error {
	if filename == "" {
		return fmt.Errorf("cannot validate WSL_ROOTFS_FILE: filename cannot be empty")
	}
	if filename == "." || filename == ".." {
		return fmt.Errorf("cannot validate WSL_ROOTFS_FILE: filename cannot be '.' or '..'")
	}
	if strings.Contains(filename, "..") {
		return fmt.Errorf("cannot validate WSL_ROOTFS_FILE: filename contains '..' path traversal sequence")
	}
	if filepath.IsAbs(filename) {
		return fmt.Errorf("cannot validate WSL_ROOTFS_FILE: filename cannot be an absolute path")
	}
	if filename != filepath.Base(filename) {
		return fmt.Errorf("cannot validate WSL_ROOTFS_FILE: filename must be a base name with no directory separators")
	}
	return nil
}

func suggestUbuntuWSLURL(rootfsURL, rootfsFile string) string {
	const brokenPrefix = "/ubuntu-wsl/daily-live/current/"
	if !strings.Contains(rootfsURL, brokenPrefix) {
		return ""
	}

	codename, _, found := strings.Cut(rootfsFile, "-wsl-")
	if !found || codename == "" {
		return ""
	}

	fixedPrefix := "/ubuntu-wsl/" + codename + "/daily-live/current/"
	return strings.Replace(rootfsURL, brokenPrefix, fixedPrefix, 1)
}

func main() {
	// Configure logging
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	// Setup context with signal handling
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx); err != nil {
		slog.Error("Fatal error", "err", err)
		os.Exit(1)
	}
}
