// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package view

import (
	"context"
	"net/http"
	"net/url"

	"miniflux.app/v2/internal/model"
)

type metaContextKey struct{}

type Metadata struct {
	Query       model.MetaQuery
	Preferences model.MetaPreferences
	UnreadURL   string
}

func WithMetadata(r *http.Request, m Metadata) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), metaContextKey{}, m))
}

func RequestMetadata(r *http.Request) Metadata {
	m, _ := r.Context().Value(metaContextKey{}).(Metadata)
	return m
}

// MetaURL preserves repeated metadata selections without copying pagination.
func MetaURL(path string, values url.Values) string {
	u, err := url.Parse(path)
	if err != nil {
		return path
	}
	q := u.Query()
	for _, key := range []string{"filter", "meta"} {
		if v, ok := values[key]; ok {
			q[key] = v
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
