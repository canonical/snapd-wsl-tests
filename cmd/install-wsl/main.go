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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
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

// fetchRelease queries the GitHub releases API for the microsoft/WSL repo.
func fetchRelease(ctx context.Context, version string) (*release, error) {
	url := "https://api.github.com/repos/microsoft/WSL/releases/tags/" + version
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, body)
	}
	var r release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("decoding release: %w", err)
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
	return nil, fmt.Errorf("no MSI asset for architecture %q in release %s", arch, r.TagName)
}

// goArchToWSLArch converts the Go runtime architecture to the WSL asset suffix.
func goArchToWSLArch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "x64"
}

// getInstalledWSLVersion runs "wsl.exe --version" and parses the version line.
// Returns an empty string if WSL is not installed or the version cannot be parsed.
func getInstalledWSLVersion(ctx context.Context) string {
	cmd := exec.CommandContext(ctx, "wsl.exe", "--version")
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	// Strip UTF-8 BOM and null bytes (the latter can appear in UTF-16LE output
	// from older WSL builds that ignore WSL_UTF8).
	s := strings.TrimPrefix(string(out), "\xef\xbb\xbf")
	s = strings.ReplaceAll(s, "\x00", "")
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		const prefix = "WSL version:"
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return ""
}

// parseVersion splits a dotted version string into numeric components.
func parseVersion(v string) []int {
	parts := strings.Split(strings.TrimSpace(v), ".")
	nums := make([]int, len(parts))
	for i, p := range parts {
		nums[i], _ = strconv.Atoi(strings.TrimSpace(p))
	}
	return nums
}

// compareVersions returns -1, 0, or 1 for a < b, a == b, a > b.
// Shorter versions are padded with zeros (so "2.6.3" == "2.6.3.0").
func compareVersions(a, b string) int {
	pa, pb := parseVersion(a), parseVersion(b)
	for len(pa) < len(pb) {
		pa = append(pa, 0)
	}
	for len(pb) < len(pa) {
		pb = append(pb, 0)
	}
	for i := range pa {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

func downloadFile(ctx context.Context, url, destPath string) error {
	fmt.Printf("Downloading %s...\n", url)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("creating %s: %w", destPath, err)
	}
	defer f.Close()
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		return fmt.Errorf("writing %s: %w", destPath, err)
	}
	fmt.Printf("Downloaded %d bytes.\n", n)
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
	fmt.Println(label)
	if err := runWSLCommand(ctx, args...); err != nil {
		fmt.Printf("WARNING: diagnostic command failed (expected in some states): %v\n", err)
	}
}

func runInstallMSI(ctx context.Context, msiPath string) error {
	err := runCommand(ctx, "msiexec.exe", "/quiet", "/passive", "/package", msiPath)
	if err == nil {
		return nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		switch exitErr.ExitCode() {
		case 1641, 3010:
			// Restart queued or required. WSL should still be usable.
			fmt.Printf("Note: msiexec exited with %d (restart pending); continuing.\n", exitErr.ExitCode())
			return nil
		}
	}
	return fmt.Errorf("msiexec /package %s: %w", msiPath, err)
}

func writeWslConfig(vmIdleTimeout, kernel, kernelCmdLine string) error {
	userProfile := os.Getenv("USERPROFILE")
	if userProfile == "" {
		return fmt.Errorf("USERPROFILE not set")
	}
	var sb strings.Builder
	sb.WriteString("[wsl2]\n")
	if vmIdleTimeout != "" {
		fmt.Fprintf(&sb, "vmIdleTimeout = %s\n", vmIdleTimeout)
	}
	if kernel != "" {
		// .wslconfig uses INI-like syntax; backslashes must be doubled.
		fmt.Fprintf(&sb, "kernel = %s\n", strings.ReplaceAll(kernel, `\`, `\\`))
	}
	if kernelCmdLine != "" {
		fmt.Fprintf(&sb, "kernelCommandLine = %s\n", kernelCmdLine)
	}
	configPath := filepath.Join(userProfile, ".wslconfig")
	fmt.Printf("Writing %s:\n%s\n", configPath, sb.String())
	return os.WriteFile(configPath, []byte(sb.String()), 0o644)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	version := os.Getenv("WSL_VERSION")
	if version == "" {
		fmt.Fprintln(os.Stderr, "Error: WSL_VERSION environment variable is required")
		os.Exit(1)
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
	fmt.Printf("Target architecture: %s\n", arch)

	installed := getInstalledWSLVersion(ctx)
	needsInstall := true
	if installed != "" {
		fmt.Printf("Detected installed WSL version: %s\n", installed)
		if compareVersions(installed, version) >= 0 {
			fmt.Printf("WSL %s is already installed (>= %s); skipping MSI installation.\n", installed, version)
			needsInstall = false
		} else {
			fmt.Printf("Upgrading WSL from %s to %s.\n", installed, version)
		}
	} else {
		fmt.Println("No WSL installation detected.")
	}

	if needsInstall {
		var msiURL, msiFilename string
		if installerURL != "" {
			msiURL = installerURL
			parts := strings.Split(msiURL, "/")
			msiFilename = parts[len(parts)-1]
			fmt.Printf("Using override installer URL: %s\n", msiURL)
		} else {
			fmt.Printf("Looking up WSL %s release on GitHub...\n", version)
			r, err := fetchRelease(ctx, version)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error fetching release: %v\n", err)
				os.Exit(1)
			}
			a, err := findMSIAsset(r, arch)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error finding MSI asset: %v\n", err)
				os.Exit(1)
			}
			msiURL = a.BrowserDownloadURL
			msiFilename = a.Name
			fmt.Printf("Found MSI asset: %s\n", msiFilename)
		}

		msiPath := filepath.Join(cacheDir, msiFilename)
		if _, err := os.Stat(msiPath); os.IsNotExist(err) {
			if err := os.MkdirAll(cacheDir, 0o755); err != nil {
				fmt.Fprintf(os.Stderr, "Error creating cache dir: %v\n", err)
				os.Exit(1)
			}
			if err := downloadFile(ctx, msiURL, msiPath); err != nil {
				fmt.Fprintf(os.Stderr, "Error downloading MSI: %v\n", err)
				os.Exit(1)
			}
		} else {
			fmt.Printf("Using cached installer: %s\n", msiPath)
		}

		fmt.Println("Running WSL installer...")
		if err := runInstallMSI(ctx, msiPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("WSL installer completed.")
	}

	fmt.Println("Switching to WSL 2.")
	if err := runWSLCommand(ctx, "--set-default-version", "2"); err != nil {
		fmt.Fprintf(os.Stderr, "Error setting WSL default version: %v\n", err)
		os.Exit(1)
	}

	runDiagnostic(ctx, "Querying WSL version.", "--version")
	runDiagnostic(ctx, "Querying WSL status.", "--status")
	runDiagnostic(ctx, "Listing registered WSL distributions.", "--list", "--verbose")
	runDiagnostic(ctx, "Listing running WSL distributions.", "--list", "--running")

	if err := writeWslConfig(vmIdleTimeout, kernel, kernelCmdLine); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing .wslconfig: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Done.")
}
