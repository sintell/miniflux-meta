// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package template // import "miniflux.app/v2/internal/template"

import (
	"html"
	"html/template"
	"regexp"
	"strings"
)

var metaBackticks = regexp.MustCompile("`+")

// metaContentValue formats backtick code spans and fenced code blocks only.
// All metadata text is escaped; the renderer itself supplies the only HTML tags.
func metaContentValue(value string) template.HTML {
	value = metaDisplayValue(value)
	runs := metaBackticks.FindAllStringIndex(value, -1)
	// Precompute equal-length closing runs to avoid repeatedly scanning unmatched
	// backticks in an application-supplied value.
	next := make([]int, len(runs))
	last := make(map[int]int)
	for i := len(runs) - 1; i >= 0; i-- {
		next[i] = -1
		width := runs[i][1] - runs[i][0]
		if j, ok := last[width]; ok {
			next[i] = j
		}
		last[width] = i
	}

	var result strings.Builder
	cursor := 0
	for i := 0; i < len(runs); i++ {
		start, end := runs[i][0], runs[i][1]
		width := end - start
		if width >= 3 {
			lineStart, lineEnd, ok := metaFenceLine(value, start, end)
			if ok && !strings.Contains(value[end:lineEnd], "`") {
				bodyStart := lineEnd
				if bodyStart < len(value) {
					bodyStart++ // Skip the opening fence's newline and optional language.
				}
				bodyEnd, fenceEnd, closing := len(value), len(value), len(runs)
				for j := i + 1; j < len(runs); j++ {
					if runs[j][1]-runs[j][0] < width {
						continue
					}
					closeStart, closeEnd, valid := metaFenceLine(value, runs[j][0], runs[j][1])
					if valid && strings.TrimSpace(value[runs[j][1]:closeEnd]) == "" {
						bodyEnd, fenceEnd, closing = closeStart, closeEnd, j
						break
					}
				}
				result.WriteString(html.EscapeString(value[cursor:lineStart]))
				result.WriteString("<pre><code>")
				result.WriteString(html.EscapeString(value[bodyStart:bodyEnd]))
				result.WriteString("</code></pre>")
				cursor, i = fenceEnd, closing
				continue
			}
		}
		if closing := next[i]; closing >= 0 {
			result.WriteString(html.EscapeString(value[cursor:start]))
			code := strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(value[end:runs[closing][0]])
			// Markdown code spans normalize line breaks and one surrounding space,
			// while preserving spans that consist entirely of spaces.
			if strings.HasPrefix(code, " ") && strings.HasSuffix(code, " ") && strings.TrimSpace(code) != "" {
				code = code[1 : len(code)-1]
			}
			result.WriteString("<code>")
			result.WriteString(html.EscapeString(code))
			result.WriteString("</code>")
			cursor, i = runs[closing][1], closing
		}
	}
	result.WriteString(html.EscapeString(value[cursor:]))
	return template.HTML(result.String())
}

// metaFenceLine accepts a fence at the start of a line with at most three spaces.
// Looking behind the delimiter is bounded, even for very long metadata lines.
func metaFenceLine(value string, start, end int) (int, int, bool) {
	lineStart := start
	for lineStart > 0 && value[lineStart-1] == ' ' && start-lineStart < 4 {
		lineStart--
	}
	if start-lineStart > 3 || lineStart > 0 && value[lineStart-1] != '\n' {
		return 0, 0, false
	}
	lineEnd := len(value)
	if offset := strings.IndexByte(value[end:], '\n'); offset >= 0 {
		lineEnd = end + offset
	}
	return lineStart, lineEnd, true
}
