// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Canonical Ltd.

package wsl

import (
	"net/http"
	"time"
)

// HTTPClient is an HTTP client with a reasonable timeout to prevent
// hanging on stalled connections. Used for API calls and downloads.
var HTTPClient = &http.Client{
	Timeout: 5 * 60 * time.Second, // 5 minutes
}

func init() {
	// Set User-Agent for GitHub API and other services
	HTTPClient.Transport = &transport{
		next: http.DefaultTransport,
	}
}

type transport struct {
	next http.RoundTripper
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone request before modifying headers to avoid mutating shared request objects
	clonedReq := req.Clone(req.Context())
	if clonedReq.Header.Get("User-Agent") == "" {
		clonedReq.Header.Set("User-Agent", "snapd-wsl-tests-actions")
	}
	return t.next.RoundTrip(clonedReq)
}
