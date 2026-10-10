// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// MetaDefinition is a user-owned, client-namespaced string field.
type MetaDefinition struct {
	ClientID      string `json:"client_id"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Description   string `json:"description"`
}

// MetaValidationError identifies invalid metadata input without including values.
type MetaValidationError string

func (e MetaValidationError) Error() string { return string(e) }

var metaIdentifier = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

func ValidateMetaIdentifier(value string) error {
	if !metaIdentifier.MatchString(value) {
		return MetaValidationError("Metadata identifiers must contain 1–64 lowercase ASCII letters, digits, underscores or hyphens")
	}
	return nil
}

func ValidateMetaText(value string) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return MetaValidationError("Metadata text must be valid UTF-8 without NUL characters")
	}
	return nil
}

func ValidateMetaField(field string) error {
	parts := strings.Split(field, ".")
	if len(parts) != 2 {
		return MetaValidationError("Metadata fields must be qualified as client_id.name")
	}
	for _, part := range parts {
		if err := ValidateMetaIdentifier(part); err != nil {
			return err
		}
	}
	return nil
}

type MetaFilter struct {
	Field   string `json:"field"`
	Pattern string `json:"pattern"`
}

type MetaQuery struct {
	Filters    []MetaFilter
	Projection []string
}

// ParseMetaQuery keeps filtering and projection independent.
func ParseMetaQuery(values url.Values) (MetaQuery, error) {
	q := MetaQuery{}
	for _, value := range values["filter"] {
		field, pattern, ok := strings.Cut(value, ":")
		if !ok {
			return q, MetaValidationError("Metadata filter must contain qualified_field:pattern")
		}
		if err := ValidateMetaField(field); err != nil {
			return q, err
		}
		if _, err := MetaLikePattern(pattern); err != nil {
			return q, err
		}
		q.Filters = append(q.Filters, MetaFilter{Field: field, Pattern: pattern})
	}
	for _, field := range values["meta"] {
		if field != "*" {
			if err := ValidateMetaField(field); err != nil {
				return q, err
			}
		}
		q.Projection = append(q.Projection, field)
	}
	return q, nil
}

// MetaLikePattern translates the documented glob language to SQL LIKE ESCAPE '\'.
// Only * is a wildcard; SQL wildcards are always escaped.
func MetaLikePattern(pattern string) (string, error) {
	if err := ValidateMetaText(pattern); err != nil {
		return "", err
	}
	var b strings.Builder
	escaped := false
	for _, ch := range pattern {
		if escaped {
			if ch != '*' && ch != '\\' {
				return "", MetaValidationError("Metadata patterns only support \\* and \\\\ escapes")
			}
			if ch == '\\' {
				b.WriteByte('\\')
			}
			b.WriteRune(ch)
			escaped = false
			continue
		}
		switch ch {
		case '\\':
			escaped = true
		case '*':
			b.WriteByte('%')
		case '%', '_':
			b.WriteByte('\\')
			b.WriteRune(ch)
		default:
			b.WriteRune(ch)
		}
	}
	if escaped {
		return "", MetaValidationError("Metadata pattern has a trailing escape")
	}
	return b.String(), nil
}

func UnregisteredMetaField(field string) error {
	return MetaValidationError(fmt.Sprintf("Metadata field '%s' is not registered", field))
}

// MetaPreferences are saved queries and ordered presentation selections, not API defaults.
type MetaPreferences struct {
	Filters       []MetaFilter `json:"filters"`
	TitleFields   []string     `json:"title_fields"`
	ContentFields []string     `json:"content_fields"`
}

func (p MetaPreferences) QueryValues() url.Values {
	v := url.Values{}
	for _, f := range p.Filters {
		v.Add("filter", f.Field+":"+f.Pattern)
	}
	return v
}
