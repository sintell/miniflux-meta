// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"unicode/utf8"

	"github.com/lib/pq"

	"miniflux.app/v2/internal/http/request"
	"miniflux.app/v2/internal/http/response"
	"miniflux.app/v2/internal/model"
	"miniflux.app/v2/internal/storage"
)

func decodeMetaJSON(r *http.Request, dst any) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return model.MetaValidationError("Metadata JSON request exceeds 4 MiB or could not be read")
	}
	if !utf8.Valid(data) {
		return model.MetaValidationError("Metadata JSON must be UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return model.MetaValidationError("Invalid metadata JSON request")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return model.MetaValidationError("Expected a single metadata JSON object")
	}
	return nil
}

// Avoid passing query patterns or database error details to the generic logger.
func metaError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	message := "Unable to perform metadata operation"
	var validation model.MetaValidationError
	if errors.As(err, &validation) {
		status = http.StatusBadRequest
		message = validation.Error()
	}
	if errors.Is(err, sql.ErrNoRows) {
		status = http.StatusNotFound
		message = "resource not found"
	}
	attrs := []any{slog.String("method", r.Method), slog.Int64("entry_id", request.RouteInt64Param(r, "entryID")), slog.Int("status", status), slog.String("reason", message), slog.String("error_type", fmt.Sprintf("%T", err))}
	var databaseError *pq.Error
	if errors.As(err, &databaseError) {
		attrs = append(attrs, slog.String("sqlstate", string(databaseError.Code)), slog.String("table", databaseError.Table), slog.String("constraint", databaseError.Constraint))
	}
	slog.Warn("Metadata operation failed", attrs...)
	body, _ := json.Marshal(map[string]string{"error_message": message})
	response.NewBuilder(w, r).WithStatus(status).WithHeader("Content-Type", "application/json").WithBodyAsBytes(body).Write()
}

func (h *handler) registerMetaHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ClientID      string  `json:"client_id"`
		Name          string  `json:"name"`
		QualifiedName string  `json:"qualified_name"`
		Description   *string `json:"description"`
	}
	if err := decodeMetaJSON(r, &input); err != nil {
		metaError(w, r, err)
		return
	}
	if input.Description == nil {
		metaError(w, r, model.MetaValidationError("Metadata description must be a string"))
		return
	}
	d := model.MetaDefinition{ClientID: input.ClientID, Name: input.Name, Description: *input.Description}
	d, created, err := h.store.RegisterMeta(request.UserID(r), d)
	if err != nil {
		metaError(w, r, err)
		return
	}
	if created {
		response.JSONCreated(w, r, d)
	} else {
		response.JSON(w, r, d)
	}
}

func (h *handler) listMetaHandler(w http.ResponseWriter, r *http.Request) {
	definitions, err := h.store.MetaRegistry(request.UserID(r), r.URL.Query().Get("client_id"))
	if err != nil {
		metaError(w, r, err)
		return
	}
	response.JSON(w, r, definitions)
}

func (h *handler) writeMetaHandler(w http.ResponseWriter, r *http.Request) {
	entryID := request.RouteInt64Param(r, "entryID")
	if entryID <= 0 {
		metaError(w, r, model.MetaValidationError("Invalid entry ID"))
		return
	}
	clientID := r.PathValue("clientID")
	name := r.PathValue("name")
	values := map[string]string{}
	remove := ""
	if r.Method == http.MethodDelete {
		remove = name
	} else if r.Method == http.MethodPut {
		var body struct {
			Value *string `json:"value"`
		}
		if err := decodeMetaJSON(r, &body); err != nil {
			metaError(w, r, err)
			return
		}
		if body.Value == nil {
			metaError(w, r, model.MetaValidationError("Metadata value must be a string"))
			return
		}
		values[name] = *body.Value
	} else {
		var body map[string]*string
		if err := decodeMetaJSON(r, &body); err != nil {
			metaError(w, r, err)
			return
		}
		if body == nil {
			metaError(w, r, model.MetaValidationError("Metadata batch must be an object"))
			return
		}
		for key, value := range body {
			if value == nil {
				metaError(w, r, model.MetaValidationError("Every metadata value must be a string"))
				return
			}
			values[key] = *value
		}
	}
	if err := h.store.WriteMeta(request.UserID(r), entryID, clientID, values, remove); err != nil {
		metaError(w, r, err)
		return
	}
	response.NoContent(w, r)
}

func (h *handler) configureMeta(w http.ResponseWriter, r *http.Request, b *storage.EntryQueryBuilder) bool {
	q, err := model.ParseMetaQuery(r.URL.Query())
	if err == nil {
		err = h.store.ValidateMetaQuery(request.UserID(r), q)
	}
	if err != nil {
		metaError(w, r, err)
		return false
	}
	b.WithMetaQuery(q)
	return true
}
