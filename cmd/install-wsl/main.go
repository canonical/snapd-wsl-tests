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
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// release is the subset of the GitHub releases API response we need.
type release struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// httpClient is an HTTP client with a reasonable timeout to prevent
// hanging on stalled connections.
var httpClient = &http.Client{
	Timeout: 5 * 60 * time.Second, // 5 minutes
}

// fetchRelease queries the GitHub releases API for the microsoft/WSL repo.
func fetchRelease(ctx context.Context, version string) (*release, error) {
	url := "https://api.github.com/repos/microsoft/WSL/releases/tags/" + version
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot look up WSL release (create request): %w", err)
	}
	req.Header.Set("User-Agent", "snapd-wsl-tests/install-wsl")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot look up WSL release (fetch): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("cannot look up WSL release (HTTP %d): %s", resp.StatusCode, body)
	}
	var r release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("cannot look up WSL release (decode response): %w", err)
	}
	return &r, nil
}

// findMSIAsset returns the platform-specific MSI asset from a release.
// Asset names follow the pattern "wsl.{version}.0.{arch}.msi".
func findMSIAsset(r *release, arch string) (*asset, error) {
	suffix := "." + arch + ".msi"
	for i := range r.Assets {
		name := r.Assets[i].Name
		if strings.HasPrefix(name, "wsl.") && strings.HasSuffix(name, suffix) {
			return &r.Assets[i], nil
		}
	}
	return nil, fmt.Errorf("cannot find MSI asset for architecture %q in release %s", arch, r.TagName)
}

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

// parseVersion splits a dotted version string into numeric components.
func parseVersion(v string) ([]int, error) {
	parts := strings.Split(strings.TrimSpace(v), ".")
	nums := make([]int, len(parts))

	for i, p := range parts {
		var err error
		nums[i], err = strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil, fmt.Errorf("parsing version component %q: %w", p, err)
		}
	}

	return nums, nil
}

// compareVersions returns -1, 0, or 1 for a < b, a == b, a > b.
// Shorter versions are padded with zeros (so "2.6.3" == "2.6.3.0").
func compareVersions(a, b string) (int, error) {
	pa, err := parseVersion(a)
	if err != nil {
		return 0, err
	}

	pb, err := parseVersion(b)
	if err != nil {
		return 0, err
	}

	for len(pa) < len(pb) {
		pa = append(pa, 0)
	}

	for len(pb) < len(pa) {
		pb = append(pb, 0)
	}

	for i := range pa {
		if pa[i] < pb[i] {
			return -1, nil
		}

		if pa[i] > pb[i] {
			return 1, nil
		}
	}

	return 0, nil
}

func downloadFile(ctx context.Context, downloadURL, destPath string) error {
	// Redact query parameters (potential SAS tokens) when logging
	parsedURL, err := neturl.Parse(downloadURL)
	if err == nil {
		parsedURL.RawQuery = ""
		slog.Info("downloading", "url", parsedURL.String())
	} else {
		slog.Info("downloading", "url", downloadURL)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("cannot download MSI file (request): %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot download MSI file (fetch): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Capture response body to include in error (may explain 403/404)
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("cannot download MSI file (HTTP %d): %s", resp.StatusCode, body)
	}
	tmpFile := destPath + ".tmp"
	f, err := os.Create(tmpFile)
	if err != nil {
		return fmt.Errorf("cannot download MSI file (create temp file): %w", err)
	}
	defer func() {
		_ = os.Remove(tmpFile) // Best effort cleanup on error
	}()
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		f.Close()
		return fmt.Errorf("cannot download MSI file (write): %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("cannot download MSI file (close): %w", err)
	}
	if err := os.Rename(tmpFile, destPath); err != nil {
		return fmt.Errorf("cannot download MSI file (rename): %w", err)
	}
	slog.Info("download complete", "bytes", n)
	return nil
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
		slog.Warn("diagnostic command failed (expected in some states)", "err", err)
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
			slog.Info("msiexec completed with restart pending", "exit_code", exitErr.ExitCode())
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

func writeWslConfig(cfg wslConfig) error {
	userProfile := os.Getenv("USERPROFILE")
	if userProfile == "" {
		return fmt.Errorf("cannot write .wslconfig: USERPROFILE not set")
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

	configPath := filepath.Join(userProfile, ".wslconfig")

	slog.Info("writing wslconfig", "path", configPath)

	return os.WriteFile(configPath, bb.Bytes(), 0o644)
}

func run(ctx context.Context) error {
	version := os.Getenv("WSL_VERSION")
	if version == "" {
		return fmt.Errorf("WSL_VERSION environment variable is required")
	}

	installerURL := os.Getenv("WSL_INSTALLER_URL")
	cacheDir := os.Getenv("WSL_MSI_CACHE_DIR")
	if cacheDir == "" {
		cacheDir = "."
	}

	vmIdleTimeout := os.Getenv("WSLCONFIG_VM_IDLE_TIMEOUT")
	kernel := os.Getenv("WSLCONFIG_KERNEL")
	kernelCmdLine := os.Getenv("WSLCONFIG_KERNEL_COMMAND_LINE")

	arch := goArchToWSLArch()
	slog.Info("target architecture", "arch", arch)

	installed, err := getInstalledWSLVersion(ctx)
	if err != nil {
		return fmt.Errorf("cannot detect installed WSL version: %w", err)
	}

	needsInstall := true
	if installed != "" {
		slog.Info("detected installed WSL version", "version", installed)
		cmp, err := compareVersions(installed, version)
		if err != nil {
			return fmt.Errorf("cannot compare versions: %w", err)
		}
		if cmp >= 0 {
			slog.Info("WSL already installed at sufficient version; skipping MSI installation",
				"installed", installed, "required", version)
			needsInstall = false
		} else {
			slog.Info("upgrading WSL", "from", installed, "to", version)
		}
	} else {
		slog.Info("no WSL installation detected")
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
			slog.Info("using override installer URL", "url", u.String())
		} else {
			slog.Info("looking up WSL release on GitHub", "version", version)
			r, err := fetchRelease(ctx, version)
			if err != nil {
				return err // fetchRelease already wraps with context
			}
			a, err := findMSIAsset(r, arch)
			if err != nil {
				return fmt.Errorf("cannot find MSI asset: %w", err)
			}
			msiURL = a.BrowserDownloadURL
			msiFilename = a.Name
			slog.Info("found MSI asset", "filename", msiFilename)
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
			if err := downloadFile(ctx, msiURL, msiPath); err != nil {
				return fmt.Errorf("cannot download WSL installer: %w", err)
			}
		} else {
			slog.Info("using cached installer", "path", msiPath)
		}

		slog.Info("running WSL installer")
		if err := runInstallMSI(ctx, msiPath); err != nil {
			return err
		}
		slog.Info("WSL installer completed")
	}

	slog.Info("switching to WSL 2")
	if err := runWSLCommand(ctx, "--set-default-version", "2"); err != nil {
		return fmt.Errorf("cannot set WSL default version: %w", err)
	}

	runDiagnostic(ctx, "querying WSL version", "--version")
	runDiagnostic(ctx, "querying WSL status", "--status")
	runDiagnostic(ctx, "listing registered WSL distributions", "--list", "--verbose")
	runDiagnostic(ctx, "listing running WSL distributions", "--list", "--running")

	if err := writeWslConfig(wslConfig{
		VMIdleTimeout:     vmIdleTimeout,
		Kernel:            kernel,
		KernelCommandLine: kernelCmdLine,
	}); err != nil {
		return fmt.Errorf("cannot write .wslconfig file: %w", err)
	}

	slog.Info("done")
	return nil
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}
