// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Canonical Ltd.

package main

import (
	"fmt"
	"strconv"
	"strings"
)

// parseVersion splits a dotted version string into numeric components.
func parseVersion(v string) ([]int, error) {
	parts := strings.Split(strings.TrimSpace(v), ".")
	nums := make([]int, len(parts))

	for i, p := range parts {
		var err error
		nums[i], err = strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil, fmt.Errorf("parsing version component %q: %w", p, err)
		}
	}

	return nums, nil
}

// compareVersions returns -1, 0, or 1 for a < b, a == b, a > b.
// Shorter versions are padded with zeros (so "2.6.3" == "2.6.3.0").
func compareVersions(a, b string) (int, error) {
	pa, err := parseVersion(a)
	if err != nil {
		return 0, err
	}

	pb, err := parseVersion(b)
	if err != nil {
		return 0, err
	}

	for len(pa) < len(pb) {
		pa = append(pa, 0)
	}

	for len(pb) < len(pa) {
		pb = append(pb, 0)
	}

	for i := range pa {
		if pa[i] < pb[i] {
			return -1, nil
		}

		if pa[i] > pb[i] {
			return 1, nil
		}
	}

	return 0, nil
}
