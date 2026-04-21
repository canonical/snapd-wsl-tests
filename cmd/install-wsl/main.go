// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Canonical Ltd.

// install-wsl installs or upgrades WSL on Windows using the Microsoft WSL
// GitHub release API. It is invoked by the install-wsl GitHub Actions
// composite action via "go run ./cmd/install-wsl".
//
// All configuration is read from environment variables:
//
//	WSL_VERSION               required; e.g. "2.6.3"
//	WSL_INSTALLER_URL         optional; overrides the GitHub release asset URL
//	WSL_MSI_CACHE_DIR         optional; directory for the downloaded MSI (default ".")
//	GITHUB_TOKEN              optional; used for GitHub API authentication
//	WSLCONFIG_VM_IDLE_TIMEOUT optional; vmIdleTimeout value for .wslconfig
//	WSLCONFIG_KERNEL          optional; kernel path for .wslconfig
//	WSLCONFIG_KERNEL_COMMAND_LINE optional; kernelCommandLine for .wslconfig
//
// Home directory is determined via os.UserHomeDir() for .wslconfig placement.
package main

import (
	"context"
	"fmt"
	"log/slog"
	neturl "net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"

	"github.com/canonical/snapd-wsl-tests/internal/dl"
)

func run(ctx context.Context) error {
	// Load all environment variables early
	version := os.Getenv("WSL_VERSION")
	if version == "" {
		return fmt.Errorf("cannot read WSL_VERSION environment variable (required)")
	}

	installerURL := os.Getenv("WSL_INSTALLER_URL")
	cacheDir := os.Getenv("WSL_MSI_CACHE_DIR")
	if cacheDir == "" {
		cacheDir = "."
	}

	vmIdleTimeout := os.Getenv("WSLCONFIG_VM_IDLE_TIMEOUT")
	kernel := os.Getenv("WSLCONFIG_KERNEL")
	kernelCmdLine := os.Getenv("WSLCONFIG_KERNEL_COMMAND_LINE")

	githubToken := os.Getenv("GITHUB_TOKEN")

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	arch := goArchToWSLArch()
	slog.Info("Target architecture", "arch", arch)

	installed, err := getInstalledWSLVersion(ctx)
	if err != nil {
		return fmt.Errorf("cannot detect installed WSL version: %w", err)
	}

	needsInstall := true
	if installed != "" {
		slog.Info("Detected installed WSL version", "version", installed)
		cmp, err := compareVersions(installed, version)
		if err != nil {
			return fmt.Errorf("cannot compare versions: %w", err)
		}
		if cmp >= 0 {
			slog.Info("WSL already installed at sufficient version; skipping MSI installation",
				"installed", installed, "required", version)
			needsInstall = false
		} else {
			slog.Info("Upgrading WSL", "from", installed, "to", version)
		}
	} else {
		slog.Info("No WSL installation detected")
	}

	if needsInstall {
		var msiURL, msiFilename string
		if installerURL != "" {
			msiURL = installerURL
			u, err := neturl.Parse(msiURL)
			if err != nil {
				return fmt.Errorf("cannot parse installer URL: %w", err)
			}
			msiFilename = path.Base(u.Path)
			if msiFilename == "" || msiFilename == "." {
				return fmt.Errorf("cannot extract filename from installer URL: %q", msiURL)
			}
			// Redact query parameters (potential SAS tokens) when logging
			u.RawQuery = ""
			slog.Info("Using override installer URL", "url", u.String())
		} else {
			slog.Info("Looking up WSL release on GitHub", "version", version)
			r, err := fetchRelease(ctx, version, githubToken)
			if err != nil {
				return err // fetchRelease already wraps with context
			}
			a, err := findMSIAsset(r, arch)
			if err != nil {
				return fmt.Errorf("cannot find MSI asset: %w", err)
			}
			msiURL = a.BrowserDownloadURL
			msiFilename = a.Name
			slog.Info("Found MSI asset", "filename", msiFilename)
		}

		msiPath := filepath.Join(cacheDir, msiFilename)
		_, statErr := os.Stat(msiPath)
		if statErr != nil {
			if !os.IsNotExist(statErr) {
				return fmt.Errorf("cannot check cache file: %w", statErr)
			}
			// File does not exist; download it.
			if err := os.MkdirAll(cacheDir, 0o755); err != nil {
				return fmt.Errorf("cannot create MSI cache directory: %w", err)
			}
			if err := dl.DownloadFile(ctx, msiURL, msiPath); err != nil {
				return fmt.Errorf("cannot download WSL installer: %w", err)
			}
		} else {
			slog.Info("Using cached installer", "path", msiPath)
		}

		slog.Info("Running WSL installer")
		if err := runInstallMSI(ctx, msiPath); err != nil {
			return err
		}
		slog.Info("WSL installer completed")
	}

	slog.Info("Switching to WSL 2")
	if err := runWSLCommand(ctx, "--set-default-version", "2"); err != nil {
		return fmt.Errorf("cannot set WSL default version: %w", err)
	}

	runDiagnostic(ctx, "Querying WSL version", "--version")
	runDiagnostic(ctx, "Querying WSL status", "--status")
	runDiagnostic(ctx, "Listing registered WSL distributions", "--list", "--verbose")
	runDiagnostic(ctx, "Listing running WSL distributions", "--list", "--running")

	if err := writeWslConfig(homeDir, wslConfig{
		VMIdleTimeout:     vmIdleTimeout,
		Kernel:            kernel,
		KernelCommandLine: kernelCmdLine,
	}); err != nil {
		return fmt.Errorf("cannot write .wslconfig file: %w", err)
	}

	slog.Info("Done")
	return nil
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Error("Fatal", "err", err)
		os.Exit(1)
	}
}
