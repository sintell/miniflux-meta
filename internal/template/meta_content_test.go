// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package template // import "miniflux.app/v2/internal/template"

import (
	"html/template"
	"strings"
	"testing"
)

func TestMetaContentValue(t *testing.T) {
	for _, tc := range []struct {
		name, value, want string
	}{
		{"plain text", "Summary\nSecond line", "Summary\nSecond line"},
		{"inline methods", "Use `show()` or `showModal()`.", "Use <code>show()</code> or <code>showModal()</code>."},
		{"literal HTML", "Native `<dialog>` and `<form method=\"dialog\">`.", "Native <code>&lt;dialog&gt;</code> and <code>&lt;form method=&#34;dialog&#34;&gt;</code>."},
		{"multiple delimiters", "Use ``a `backtick` here``.", "Use <code>a `backtick` here</code>."},
		{"unequal delimiters", "Keep `unmatched`` literal.", "Keep `unmatched`` literal."},
		{"padded span", "` <dialog> `", "<code>&lt;dialog&gt;</code>"},
		{"spaces span", "`   `", "<code>   </code>"},
		{"multiline span", "`show()\r\nand close()`", "<code>show() and close()</code>"},
		{"fenced code", "Example:\n```js\ndialog.showModal();\n```\nDone.", "Example:\n<pre><code>dialog.showModal();\n</code></pre>\nDone."},
		{"longer closing fence", "````html\n<dialog>`literal`</dialog>\n`````", "<pre><code>&lt;dialog&gt;`literal`&lt;/dialog&gt;\n</code></pre>"},
		{"indented fence", "   ```\ntext\n  ```", "<pre><code>text\n</code></pre>"},
		{"unclosed fence", "```\nremaining text", "<pre><code>remaining text</code></pre>"},
		{"CRLF fence", "```js\r\nshow();\r\n```\r\n", "<pre><code>show();\r\n</code></pre>\n"},
		{"untrusted HTML", "<script>alert(1)</script> `<img onerror=alert(1)>`", "&lt;script&gt;alert(1)&lt;/script&gt; <code>&lt;img onerror=alert(1)&gt;</code>"},
		{"untrusted fence label", "```\" onmouseover=\"alert(1)\n</code><script>alert(1)</script>\n```", "<pre><code>&lt;/code&gt;&lt;script&gt;alert(1)&lt;/script&gt;\n</code></pre>"},
		{"topic lists", `["AI","Business"]`, "AI, Business"},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(metaContentValue(tc.value)); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMetaCodeFormattingIsRestrictedToContent(t *testing.T) {
	tmpl, err := template.New("metadata").Funcs((&funcMap{}).Map()).Parse(
		`<span title="{{.}}">{{metaDisplayValue .}}</span><div>{{metaContentValue .}}</div>`)
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := tmpl.Execute(&output, "Use `<dialog>`."); err != nil {
		t.Fatal(err)
	}
	want := "<span title=\"Use `&lt;dialog&gt;`.\">Use `&lt;dialog&gt;`.</span><div>Use <code>&lt;dialog&gt;</code>.</div>"
	if output.String() != want {
		t.Fatalf("got %q, want %q", output.String(), want)
	}
}
