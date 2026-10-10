// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package request

import "net/http"

// LogURI omits metadata patterns, which may contain private article annotations.
func LogURI(r *http.Request) string {
	if !r.URL.Query().Has("filter") {
		return r.RequestURI
	}
	u := *r.URL
	q := u.Query()
	q.Set("filter", "[redacted]")
	u.RawQuery = q.Encode()
	return u.RequestURI()
}
