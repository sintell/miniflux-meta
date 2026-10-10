// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"errors"
	"net/http"
	"strings"

	"miniflux.app/v2/internal/http/request"
	"miniflux.app/v2/internal/http/response"
	"miniflux.app/v2/internal/model"
	"miniflux.app/v2/internal/ui/view"
)

// Only authenticated UI requests get presentation preferences. Explicit URL
// filters are never replaced with the saved Unread shortcut.
func (h *handler) withMetadata(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicRoute(r) {
			next.ServeHTTP(w, r)
			return
		}
		q, err := model.ParseMetaQuery(r.URL.Query())
		if err == nil {
			err = h.store.ValidateMetaQuery(request.UserID(r), q)
		}
		if err != nil {
			var validation model.MetaValidationError
			if errors.As(err, &validation) {
				http.Error(w, validation.Error(), http.StatusBadRequest)
			} else {
				response.HTMLServerError(w, r, err)
			}
			return
		}
		p, err := h.store.MetaPreferences(request.UserID(r))
		if err != nil {
			response.HTMLServerError(w, r, err)
			return
		}
		q.Projection = append(q.Projection, p.TitleFields...)
		if strings.Contains(r.URL.Path, "/entry/") {
			q.Projection = append(q.Projection, p.ContentFields...)
		}
		r = view.WithMetadata(r, view.Metadata{Query: q, Preferences: p, UnreadURL: view.MetaURL(h.routePath("/unread"), p.QueryValues())})
		next.ServeHTTP(w, r)
	})
}

func metaQuery(r *http.Request) model.MetaQuery { return view.RequestMetadata(r).Query }
