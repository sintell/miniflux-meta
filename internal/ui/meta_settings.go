// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"errors"
	"net/http"

	"miniflux.app/v2/internal/http/request"
	"miniflux.app/v2/internal/http/response"
	"miniflux.app/v2/internal/model"
	"miniflux.app/v2/internal/ui/view"
)

func (h *handler) metaSettings(w http.ResponseWriter, r *http.Request) {
	user, err := h.store.UserByID(request.UserID(r))
	if err != nil {
		response.HTMLServerError(w, r, err)
		return
	}
	p := view.RequestMetadata(r).Preferences
	var validation error
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form", 400)
			return
		}
		p = model.MetaPreferences{}
		fields, patterns := r.PostForm["filter_field"], r.PostForm["filter_pattern"]
		if len(fields) != len(patterns) {
			http.Error(w, "Invalid filter rows", 400)
			return
		}
		for i, field := range fields {
			if field != "" {
				p.Filters = append(p.Filters, model.MetaFilter{Field: field, Pattern: patterns[i]})
			}
		}
		for _, field := range r.PostForm["title_field"] {
			if field != "" {
				p.TitleFields = append(p.TitleFields, field)
			}
		}
		for _, field := range r.PostForm["content_field"] {
			if field != "" {
				p.ContentFields = append(p.ContentFields, field)
			}
		}
		validation = h.store.SaveMetaPreferences(user.ID, p)
		if validation == nil {
			response.HTMLRedirect(w, r, h.routePath("/settings/meta"))
			return
		}
		var inputError model.MetaValidationError
		if !errors.As(validation, &inputError) {
			response.HTMLServerError(w, r, validation)
			return
		}
	}
	definitions, err := h.store.MetaRegistry(user.ID, "")
	if err != nil {
		response.HTMLServerError(w, r, err)
		return
	}
	nav, err := h.store.GetNavMetadata(user.ID)
	if err != nil {
		response.HTMLServerError(w, r, err)
		return
	}
	v := view.New(h.tpl, r)
	v.Set("user", user).Set("menu", "settings").Set("definitions", definitions).Set("preferences", p)
	v.Set("countUnread", nav.CountUnread).Set("countErrorFeeds", nav.CountErrorFeeds)
	if validation != nil {
		v.Set("errorMessage", validation.Error())
	}
	response.HTML(w, r, v.Render("meta_settings"))
}
