// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Canonical Ltd.

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"os"
)

func downloadFile(ctx context.Context, downloadURL, destPath string) error {
	// Redact query parameters (potential SAS tokens) when logging
	parsedURL, err := neturl.Parse(downloadURL)
	if err == nil {
		parsedURL.RawQuery = ""
		slog.Info("Downloading", "url", parsedURL.String())
	} else {
		slog.Info("Downloading", "url", downloadURL)
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
	slog.Info("Download complete", "bytes", n)
	return nil
}
