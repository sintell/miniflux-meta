// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// MetaDefinition describes a user-owned client namespace field. All values are strings.
type MetaDefinition struct {
	ClientID      string `json:"client_id"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name,omitempty"`
	Description   string `json:"description"`
}

func (c *Client) RegisterMeta(d MetaDefinition) (*MetaDefinition, error) {
	ctx, cancel := withDefaultTimeout()
	defer cancel()
	return c.RegisterMetaContext(ctx, d)
}

func (c *Client) RegisterMetaContext(ctx context.Context, d MetaDefinition) (*MetaDefinition, error) {
	body, err := c.request.Post(ctx, "/v1/meta/register", d)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var result MetaDefinition
	err = json.NewDecoder(body).Decode(&result)
	return &result, err
}

func (c *Client) MetaRegistry(clientID string) ([]MetaDefinition, error) {
	ctx, cancel := withDefaultTimeout()
	defer cancel()
	return c.MetaRegistryContext(ctx, clientID)
}

func (c *Client) MetaRegistryContext(ctx context.Context, clientID string) ([]MetaDefinition, error) {
	body, err := c.request.Get(ctx, "/v1/meta?"+url.Values{"client_id": {clientID}}.Encode())
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var result []MetaDefinition
	err = json.NewDecoder(body).Decode(&result)
	return result, err
}

func (c *Client) SetEntryMeta(entryID int64, clientID, name, value string) error {
	ctx, cancel := withDefaultTimeout()
	defer cancel()
	return c.SetEntryMetaContext(ctx, entryID, clientID, name, value)
}

func (c *Client) SetEntryMetaContext(ctx context.Context, entryID int64, clientID, name, value string) error {
	_, err := c.request.Put(ctx, metaPath(entryID, clientID)+"/"+url.PathEscape(name), map[string]string{"value": value})
	return err
}

func (c *Client) UpdateEntryMeta(entryID int64, clientID string, values map[string]string) error {
	ctx, cancel := withDefaultTimeout()
	defer cancel()
	return c.UpdateEntryMetaContext(ctx, entryID, clientID, values)
}

func (c *Client) UpdateEntryMetaContext(ctx context.Context, entryID int64, clientID string, values map[string]string) error {
	_, err := c.request.execute(ctx, http.MethodPatch, metaPath(entryID, clientID), values)
	return err
}

func (c *Client) DeleteEntryMeta(entryID int64, clientID, name string) error {
	ctx, cancel := withDefaultTimeout()
	defer cancel()
	return c.DeleteEntryMetaContext(ctx, entryID, clientID, name)
}

func (c *Client) DeleteEntryMetaContext(ctx context.Context, entryID int64, clientID, name string) error {
	return c.request.Delete(ctx, metaPath(entryID, clientID)+"/"+url.PathEscape(name))
}

func (c *Client) EntryWithMeta(entryID int64, fields ...string) (*Entry, error) {
	ctx, cancel := withDefaultTimeout()
	defer cancel()
	return c.EntryWithMetaContext(ctx, entryID, fields...)
}

func (c *Client) EntryWithMetaContext(ctx context.Context, entryID int64, fields ...string) (*Entry, error) {
	body, err := c.request.Get(ctx, fmt.Sprintf("/v1/entries/%d?", entryID)+url.Values{"meta": fields}.Encode())
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var entry Entry
	err = json.NewDecoder(body).Decode(&entry)
	return &entry, err
}

func metaPath(entryID int64, clientID string) string {
	return fmt.Sprintf("/v1/entries/%d/meta/%s", entryID, url.PathEscape(clientID))
}
