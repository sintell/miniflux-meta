// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"miniflux.app/v2/internal/http/request"
	"miniflux.app/v2/internal/http/response"
)

func (h *handler) markCollectionAsRead(w http.ResponseWriter, r *http.Request) {
	userID := request.UserID(r)
	b := h.store.NewEntryQueryBuilder(userID).WithMetaQuery(metaQuery(r))
	scope := strings.Split(strings.Trim(r.URL.Query().Get("scope"), "/"), "/")
	valid := false
	if len(scope) == 1 {
		switch scope[0] {
		case "unread":
			b.WithGloballyVisible()
			valid = true
		case "starred":
			b.WithStarred(true)
			valid = true
		case "history":
			b.WithStatuses("read")
			valid = true
		case "search":
			if q := r.URL.Query().Get("q"); q != "" {
				b.WithSearchQuery(q)
				valid = true
			}
		}
	} else if len(scope) >= 3 {
		if scope[0] == "tags" && len(scope) == 4 && scope[2] == "entries" && scope[3] == "all" {
			if tag, err := url.PathUnescape(scope[1]); err == nil {
				b.WithTags(tag)
				valid = true
			}
		} else if scope[2] == "entries" && (len(scope) == 3 || len(scope) == 4 && (scope[3] == "all" || scope[0] == "category" && scope[3] == "starred")) {
			id, err := strconv.ParseInt(scope[1], 10, 64)
			if err == nil && id > 0 {
				switch scope[0] {
				case "feed":
					checkedAt, err := h.store.CheckedAt(userID, id)
					if err != nil {
						response.HTMLNotFound(w, r)
						return
					}
					b.WithFeedID(id).BeforePublishedDate(checkedAt)
					valid = true
				case "category":
					b.WithCategoryID(id).BeforePublishedDate(time.Now())
					valid = true
					if len(scope) == 4 && scope[3] == "starred" {
						b.WithStarred(true)
					}
				}
			}
		}
	}
	if !valid {
		http.Error(w, "Invalid article collection", http.StatusBadRequest)
		return
	}
	if err := b.MarkMatchingAsRead(); err != nil {
		response.JSONServerError(w, r, err)
		return
	}
	response.JSON(w, r, "OK")
}

func (h *handler) metaUnreadCount(w http.ResponseWriter, r *http.Request) {
	nav, err := h.store.GetNavMetadata(request.UserID(r))
	if err != nil {
		response.JSONServerError(w, r, err)
		return
	}
	response.JSON(w, r, map[string]int{"count": nav.CountUnread})
}
