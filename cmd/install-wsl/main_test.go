// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Canonical Ltd.

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		version string
		want    []int
		wantErr bool
	}{
		{"2.6.3", []int{2, 6, 3}, false},
		{"2.6.3.0", []int{2, 6, 3, 0}, false},
		{"1.2", []int{1, 2}, false},
		{"10.20.30", []int{10, 20, 30}, false},
		{"not.a.version", []int{0, 0, 0}, true},
		{"1.a.2", []int{1, 0, 2}, true},
		{"", []int{0}, true}, // Empty string should error
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			got, err := parseVersion(tt.version)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseVersion(%q) error = %v, wantErr %v", tt.version, err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(got) != len(tt.want) {
				t.Errorf("parseVersion(%q) len = %d, want %d", tt.version, len(got), len(tt.want))
				return
			}
			for i := range tt.want {
				if i < len(got) && got[i] != tt.want[i] {
					t.Errorf("parseVersion(%q)[%d] = %d, want %d", tt.version, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a    string
		b    string
		want int // -1, 0, or 1
	}{
		{"2.6.3", "2.6.3", 0},
		{"2.6.3", "2.6.3.0", 0},
		{"2.6.3.0", "2.6.3", 0},
		{"2.6.4", "2.6.3", 1},
		{"2.6.3", "2.6.4", -1},
		{"3.0", "2.99", 1},
		{"1.0.0", "1.0.1", -1},
		{"2.2.4", "2.6.3", -1},
		{"2.6.3", "2.2.4", 1},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s vs %s", tt.a, tt.b), func(t *testing.T) {
			got, err := compareVersions(tt.a, tt.b)
			if err != nil {
				t.Errorf("compareVersions(%q, %q) error = %v", tt.a, tt.b, err)
				return
			}
			if got != tt.want {
				t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestWriteWslConfig(t *testing.T) {
	tests := []struct {
		name  string
		cfg   wslConfig
		wants []string // Substrings that should appear in file
	}{
		{
			name: "empty config",
			cfg: wslConfig{
				VMIdleTimeout:     "",
				Kernel:            "",
				KernelCommandLine: "",
			},
			wants: []string{"[wsl2]\n"},
		},
		{
			name: "with idle timeout",
			cfg: wslConfig{
				VMIdleTimeout:     "60000",
				Kernel:            "",
				KernelCommandLine: "",
			},
			wants: []string{"[wsl2]", "vmIdleTimeout = 60000"},
		},
		{
			name: "with kernel path",
			cfg: wslConfig{
				VMIdleTimeout:     "",
				Kernel:            `C:\path\to\kernel`,
				KernelCommandLine: "",
			},
			wants: []string{"[wsl2]", `kernel = C:\\path\\to\\kernel`},
		},
		{
			name: "with all settings",
			cfg: wslConfig{
				VMIdleTimeout:     "120000",
				Kernel:            `D:\vmlinux`,
				KernelCommandLine: "security=apparmor",
			},
			wants: []string{"[wsl2]", "vmIdleTimeout = 120000", `kernel = D:\\vmlinux`, "kernelCommandLine = security=apparmor"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			err := writeWslConfig(tmpDir, tt.cfg)
			if err != nil {
				t.Fatalf("writeWslConfig(%q, %+v) error = %v", tmpDir, tt.cfg, err)
			}

			configPath := filepath.Join(tmpDir, ".wslconfig")
			content, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatalf("failed to read .wslconfig: %v", err)
			}

			configStr := string(content)
			for _, want := range tt.wants {
				if !contains(configStr, want) {
					t.Errorf("writeWslConfig output missing %q:\n%s", want, configStr)
				}
			}
		})
	}
}

func TestDownloadFile(t *testing.T) {
	tests := []struct {
		name     string
		setupSrv func(*httptest.Server)
		wantErr  bool
		wantData string
	}{
		{
			name: "successful download",
			setupSrv: func(srv *httptest.Server) {
				srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusOK)
					fmt.Fprint(w, "test MSI content")
				})
			},
			wantErr:  false,
			wantData: "test MSI content",
		},
		{
			name: "http error",
			setupSrv: func(srv *httptest.Server) {
				srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				})
			},
			wantErr: true,
		},
		{
			name: "server error",
			setupSrv: func(srv *httptest.Server) {
				srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				})
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(nil)
			defer srv.Close()
			tt.setupSrv(srv)

			tmpDir := t.TempDir()
			destPath := filepath.Join(tmpDir, "test.msi")

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			err := downloadFile(ctx, srv.URL, destPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("downloadFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				content, err := os.ReadFile(destPath)
				if err != nil {
					t.Fatalf("failed to read downloaded file: %v", err)
				}
				if string(content) != tt.wantData {
					t.Errorf("downloadFile() content = %q, want %q", string(content), tt.wantData)
				}

				// Verify no temp file left behind
				tmpFile := destPath + ".tmp"
				if _, err := os.Stat(tmpFile); err == nil {
					t.Errorf("temp file %s still exists after successful download", tmpFile)
				}
			}
		})
	}
}

func TestDownloadFileAtomicRename(t *testing.T) {
	// Test that partial file is not left in cache on error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Write some data then close (simulating connection drop)
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "incomplete")
		// Handler returns, simulating connection close
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "test.msi")

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_ = downloadFile(ctx, srv.URL, destPath)

	// Verify destination file does NOT exist (partial write not committed)
	if _, err := os.Stat(destPath); err == nil {
		t.Errorf("destination file exists after failed download; atomic rename failed")
	}

	// Verify no temp file left behind
	tmpFile := destPath + ".tmp"
	if _, err := os.Stat(tmpFile); err == nil {
		t.Errorf("temp file %s still exists after failed download", tmpFile)
	}
}

// Helper function for string searching
func contains(s, substr string) bool {
	return len(substr) == 0 || (len(s) > 0 && len(s) >= len(substr) && stringContains(s, substr))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
