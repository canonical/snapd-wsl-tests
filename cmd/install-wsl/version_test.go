// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Canonical Ltd.

package main

import (
	"fmt"
	"testing"
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
