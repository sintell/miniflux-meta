// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package request

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLogURIHidesMetadataPatterns(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/entries?filter=app.private:secret&filter=other.private:also-secret&status=unread", nil)
	if got := LogURI(r); strings.Contains(got, "secret") || !strings.Contains(got, "status=unread") {
		t.Fatal(got)
	}
	if !strings.Contains(r.URL.RawQuery, "secret") {
		t.Fatal("logging mutated the query")
	}
}
