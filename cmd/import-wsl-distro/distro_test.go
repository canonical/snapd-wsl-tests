// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Canonical Ltd.

package main

import (
	"testing"
)

func TestParseWslConf(t *testing.T) {
	tests := []struct {
		name         string
		content      []byte
		wantSections map[string]map[string]string
	}{
		{
			name:         "empty config",
			content:      []byte(""),
			wantSections: map[string]map[string]string{},
		},
		{
			name:    "config with boot section",
			content: []byte("[boot]\nsystemd=true\n"),
			wantSections: map[string]map[string]string{
				"boot": {
					"systemd": "true",
				},
			},
		},
		{
			name:    "config with multiple sections",
			content: []byte("[boot]\nsystemd=true\n[interop]\nappendWindowsPath=true\n"),
			wantSections: map[string]map[string]string{
				"boot": {
					"systemd": "true",
				},
				"interop": {
					"appendWindowsPath": "true",
				},
			},
		},
		{
			name:    "config with comments",
			content: []byte("# Comment\n[boot]\nsystemd=true\n# Another comment\n"),
			wantSections: map[string]map[string]string{
				"boot": {
					"systemd": "true",
				},
			},
		},
		{
			name:    "config with whitespace",
			content: []byte("  [boot]  \n  systemd  =  true  \n"),
			wantSections: map[string]map[string]string{
				"boot": {
					"systemd": "true",
				},
			},
		},
		{
			name:    "config with CRLF line endings",
			content: []byte("[boot]\r\nsystemd=true\r\n"),
			wantSections: map[string]map[string]string{
				"boot": {
					"systemd": "true",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseWslConf(tt.content)

			// Check all expected sections exist
			for sectionName, expectedKVs := range tt.wantSections {
				section, exists := result[sectionName]
				if !exists {
					t.Errorf("Expected section %q not found", sectionName)
					return
				}

				// Check all expected key-value pairs
				for key, expectedValue := range expectedKVs {
					if value, ok := section[key]; !ok {
						t.Errorf("Expected key %q in section %q not found", key, sectionName)
					} else if value != expectedValue {
						t.Errorf("Section %q key %q: got %q, want %q", sectionName, key, value, expectedValue)
					}
				}
			}
		})
	}
}

func TestIsDistroNotFoundError(t *testing.T) {
	tests := []struct {
		name    string
		errMsg  string
		wantErr bool
	}{
		{
			name:    "not found error",
			errMsg:  "exit status 1: not found",
			wantErr: true,
		},
		{
			name:    "no distribution error",
			errMsg:  "exit status 1: no distribution with the supplied name",
			wantErr: true,
		},
		{
			name:    "there is no distribution error",
			errMsg:  "there is no distribution registered",
			wantErr: true,
		},
		{
			name:    "other error",
			errMsg:  "some other error",
			wantErr: false,
		},
		{
			name:    "nil error",
			errMsg:  "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.errMsg != "" {
				err = &mockError{msg: tt.errMsg}
			}
			result := isDistroNotFoundError(err)
			if result != tt.wantErr {
				t.Errorf("isDistroNotFoundError(%v) = %v, want %v", tt.errMsg, result, tt.wantErr)
			}
		})
	}
}

// mockError is a simple error implementation for testing
type mockError struct {
	msg string
}

func (e *mockError) Error() string {
	return e.msg
}
