// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Canonical Ltd.

package main

import "testing"

func TestSuggestUbuntuWSLURL(t *testing.T) {
	tests := []struct {
		name       string
		rootfsURL  string
		rootfsFile string
		want       string
	}{
		{
			name:       "rewrites legacy daily-live URL with codename",
			rootfsURL:  "https://cdimage.ubuntu.com/ubuntu-wsl/daily-live/current/resolute-wsl-amd64.wsl",
			rootfsFile: "resolute-wsl-amd64.wsl",
			want:       "https://cdimage.ubuntu.com/ubuntu-wsl/resolute/daily-live/current/resolute-wsl-amd64.wsl",
		},
		{
			name:       "no rewrite when URL already uses codename path",
			rootfsURL:  "https://cdimage.ubuntu.com/ubuntu-wsl/resolute/daily-live/current/resolute-wsl-amd64.wsl",
			rootfsFile: "resolute-wsl-amd64.wsl",
			want:       "",
		},
		{
			name:       "no rewrite when file does not expose codename",
			rootfsURL:  "https://cdimage.ubuntu.com/ubuntu-wsl/daily-live/current/custom-image.wsl",
			rootfsFile: "custom-image.wsl",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := suggestUbuntuWSLURL(tt.rootfsURL, tt.rootfsFile)
			if got != tt.want {
				t.Fatalf("suggestUbuntuWSLURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
