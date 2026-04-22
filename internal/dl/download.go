// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Canonical Ltd.

// Package dl provides file download utilities for GitHub Actions.
package dl

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"os"

	"github.com/canonical/snapd-wsl-tests/internal/wsl"
)

// DownloadFile downloads a file from downloadURL and saves it to destPath.
// It uses atomic operations: downloads to a temporary file (.tmp extension),
// then renames to the final path only on success. This prevents partial
// files from being cached on network failures.
func DownloadFile(ctx context.Context, downloadURL, destPath string) error {
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
		return fmt.Errorf("cannot download file (request): %w", err)
	}
	resp, err := wsl.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot download file (fetch): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Capture a bounded amount of the response body to include in the error
		// (may explain 403/404) without allowing excessive memory usage.
		const maxErrorBodyBytes = 4096
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes+1))
		truncated := len(body) > maxErrorBodyBytes
		if truncated {
			body = body[:maxErrorBodyBytes]
		}
		suffix := ""
		if truncated {
			suffix = " (truncated)"
		}
		return fmt.Errorf("cannot download file (HTTP %d): %q%s", resp.StatusCode, body, suffix)
	}
	tmpFile := destPath + ".tmp"
	f, err := os.Create(tmpFile)
	if err != nil {
		return fmt.Errorf("cannot download file (create temp file): %w", err)
	}
	defer func() {
		_ = os.Remove(tmpFile) // Best effort cleanup on error
	}()
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		f.Close()
		return fmt.Errorf("cannot download file (write): %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("cannot download file (close): %w", err)
	}
	if err := os.Rename(tmpFile, destPath); err != nil {
		// On Windows, os.Rename fails if destination exists.
		// Best-effort remove and retry.
		if os.IsExist(err) {
			if rmErr := os.Remove(destPath); rmErr == nil {
				if err := os.Rename(tmpFile, destPath); err != nil {
					return fmt.Errorf("cannot download file (rename after remove): %w", err)
				}
			} else {
				// If we can't remove the destination, return the original error
				return fmt.Errorf("cannot download file (rename): %w", err)
			}
		} else {
			return fmt.Errorf("cannot download file (rename): %w", err)
		}
	}
	slog.Info("Download complete", "bytes", n)
	return nil
}
