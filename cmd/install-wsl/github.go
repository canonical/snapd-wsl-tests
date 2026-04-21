// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Canonical Ltd.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
func fetchRelease(ctx context.Context, version, token string) (*release, error) {
	url := "https://api.github.com/repos/microsoft/WSL/releases/tags/" + version
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot look up WSL release (create request): %w", err)
	}
	req.Header.Set("User-Agent", "snapd-wsl-tests/install-wsl")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
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
