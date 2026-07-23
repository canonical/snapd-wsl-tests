// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Canonical Ltd.

package dl

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

func TestCheckURLReachable(t *testing.T) {
	t.Run("head success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodHead {
				t.Fatalf("unexpected method %q", r.Method)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := CheckURLReachable(ctx, srv.URL); err != nil {
			t.Fatalf("CheckURLReachable() error = %v", err)
		}
	})

	t.Run("head method not allowed falls back to ranged get", func(t *testing.T) {
		sawGet := false
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodHead:
				w.WriteHeader(http.StatusMethodNotAllowed)
			case http.MethodGet:
				sawGet = true
				if got := r.Header.Get("Range"); got != "bytes=0-0" {
					t.Fatalf("Range header = %q, want %q", got, "bytes=0-0")
				}
				w.WriteHeader(http.StatusPartialContent)
				fmt.Fprint(w, "x")
			default:
				t.Fatalf("unexpected method %q", r.Method)
			}
		}))
		defer srv.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := CheckURLReachable(ctx, srv.URL); err != nil {
			t.Fatalf("CheckURLReachable() error = %v", err)
		}
		if !sawGet {
			t.Fatal("expected fallback GET request")
		}
	})

	t.Run("404 returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, "not found")
		}))
		defer srv.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := CheckURLReachable(ctx, srv.URL)
		if err == nil {
			t.Fatal("CheckURLReachable() expected error, got nil")
		}
	})
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
					fmt.Fprint(w, "test file content")
				})
			},
			wantErr:  false,
			wantData: "test file content",
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
			destPath := filepath.Join(tmpDir, "test.bin")

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			err := DownloadFile(ctx, srv.URL, destPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("DownloadFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				content, err := os.ReadFile(destPath)
				if err != nil {
					t.Fatalf("failed to read downloaded file: %v", err)
				}
				if string(content) != tt.wantData {
					t.Errorf("DownloadFile() content = %q, want %q", string(content), tt.wantData)
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
	destPath := filepath.Join(tmpDir, "test.bin")

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_ = DownloadFile(ctx, srv.URL, destPath)

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
