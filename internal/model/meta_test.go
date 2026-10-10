// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestMetaPatterns(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"7.*", "7.%"}, {"*end", "%end"}, {"a*b", "a%b"}, {"*", "%"}, {"", ""},
		{"%_", `\%\_`}, {`\*`, `*`}, {`\\`, `\\`}, {"Привет:*", "Привет:%"}, {"[a].+", "[a].+"},
	} {
		got, err := MetaLikePattern(tc.input)
		if err != nil || got != tc.want {
			t.Errorf("%q: got %q, %v; want %q", tc.input, got, err, tc.want)
		}
	}
	for _, pattern := range []string{`\`, `\x`} {
		if _, err := MetaLikePattern(pattern); err == nil {
			t.Errorf("accepted %q", pattern)
		}
	}
	q, err := ParseMetaQuery(url.Values{"filter": {"a.reference:https://example.org/*", "b.name:*"}, "meta": {"a.summary", "*"}})
	if err != nil || len(q.Filters) != 2 || q.Filters[0].Pattern != "https://example.org/*" || len(q.Projection) != 2 {
		t.Fatalf("parse: %#v, %v", q, err)
	}
	for _, field := range []string{"rating", "a.b.c", "A.b", "a.💥", "a."} {
		if ValidateMetaField(field) == nil {
			t.Errorf("accepted %q", field)
		}
	}
}

func TestMetaProjectionJSON(t *testing.T) {
	e := NewEntry()
	b, _ := json.Marshal(e)
	var obj map[string]any
	json.Unmarshal(b, &obj)
	if _, ok := obj["meta"]; ok {
		t.Fatal("default projection included meta")
	}
	m := map[string]string{}
	e.Meta = &m
	b, _ = json.Marshal(e)
	json.Unmarshal(b, &obj)
	if v, ok := obj["meta"].(map[string]any); !ok || len(v) != 0 {
		t.Fatalf("empty projection: %s", b)
	}
}
