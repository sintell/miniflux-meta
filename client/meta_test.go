// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"net/http"
	"net/url"
	"reflect"
	"testing"
)

func TestMetadataClient(t *testing.T) {
	values := map[string]string{"app.summary": "unicode: Привет\n<text>"}
	c := NewClientWithOptions("http://mf", WithHTTPClient(newFakeHTTPClient(t, func(t *testing.T, r *http.Request) *http.Response {
		if r.Method == http.MethodPatch {
			expectRequest(t, http.MethodPatch, "http://mf/v1/entries/12/meta/app", nil, r)
			expectFromJSON(t, r.Body, &map[string]string{"summary": "Привет\n<text>"})
			return jsonResponseFrom(t, http.StatusNoContent, http.Header{}, nil)
		}
		if !reflect.DeepEqual(r.URL.Query()["meta"], []string{"app.summary", "other.field"}) {
			t.Fatal(r.URL)
		}
		return jsonResponseFrom(t, http.StatusOK, http.Header{}, &Entry{ID: 12, Meta: &values})
	})))
	if err := c.UpdateEntryMeta(12, "app", map[string]string{"summary": "Привет\n<text>"}); err != nil {
		t.Fatal(err)
	}
	e, err := c.EntryWithMeta(12, "app.summary", "other.field")
	if err != nil || e.Meta == nil || !reflect.DeepEqual(*e.Meta, values) {
		t.Fatal(e, err)
	}
	filters := []string{"app.reference:https://example.org/*", "app.name:Привет + &"}
	u, err := url.Parse(buildFilterQueryString("/v1/entries", &Filter{Filters: filters, Meta: []string{"app.summary", "*"}}))
	if err != nil || !reflect.DeepEqual(u.Query()["filter"], filters) || len(u.Query()["meta"]) != 2 {
		t.Fatal(u, err)
	}
	u, err = url.Parse(buildEntryIDsFilterQueryString("/v1/entries/ids", &EntryIDsFilter{Filters: filters}))
	if err != nil || !reflect.DeepEqual(u.Query()["filter"], filters) {
		t.Fatal(u, err)
	}
}
