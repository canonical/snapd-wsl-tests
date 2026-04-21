// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Canonical Ltd.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

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
