// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package ui // import "miniflux.app/v2/internal/ui"

import (
	"net/url"
	"strconv"
)

type pagination struct {
	Route        string
	SearchQuery  string
	Total        int
	Offset       int
	ItemsPerPage int
	NextOffset   int
	LastOffset   int
	PrevOffset   int
	FirstOffset  int
	UnreadOnly   bool
	ShowNext     bool
	ShowLast     bool
	ShowFirst    bool
	ShowPrev     bool
	Query        url.Values
}

func (p pagination) WithQuery(q url.Values) any { p.Query = q; return p }

func (p pagination) URL(offset int) string {
	q := url.Values{}
	for key, values := range p.Query {
		q[key] = append([]string{}, values...)
	}
	q.Set("offset", strconv.Itoa(offset))
	if p.SearchQuery != "" {
		q.Set("q", p.SearchQuery)
	}
	if p.UnreadOnly {
		q.Set("unread", "1")
	}
	return p.Route + "?" + q.Encode()
}

func getPagination(route string, total, offset, nbItemsPerPage int) pagination {
	nextOffset := 0
	prevOffset := 0

	firstOffset := 0
	lastOffset := (total / nbItemsPerPage) * nbItemsPerPage
	if lastOffset == total {
		lastOffset -= nbItemsPerPage
	}

	showNext := (total - offset) > nbItemsPerPage
	showPrev := offset > 0
	showLast := showNext
	showFirst := showPrev

	if showNext {
		nextOffset = offset + nbItemsPerPage
	}

	if showPrev {
		prevOffset = offset - nbItemsPerPage
	}

	return pagination{
		Route:        route,
		Total:        total,
		Offset:       offset,
		ItemsPerPage: nbItemsPerPage,
		ShowNext:     showNext,
		ShowLast:     showLast,
		NextOffset:   nextOffset,
		LastOffset:   lastOffset,
		ShowPrev:     showPrev,
		ShowFirst:    showFirst,
		PrevOffset:   prevOffset,
		FirstOffset:  firstOffset,
	}
}
