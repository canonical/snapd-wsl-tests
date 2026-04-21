// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Canonical Ltd.

package main

// contains is a helper function for testing that checks if a string contains a substring.
func contains(s, substr string) bool {
	return len(substr) == 0 || (len(s) > 0 && len(s) >= len(substr) && stringContains(s, substr))
}

// stringContains performs a naive substring search.
func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
