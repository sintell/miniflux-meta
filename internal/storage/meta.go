// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/lib/pq"
	"miniflux.app/v2/internal/model"
)

func (s *Storage) RegisterMeta(userID int64, d model.MetaDefinition) (model.MetaDefinition, bool, error) {
	if err := model.ValidateMetaText(d.Description); err != nil {
		return d, false, err
	}
	if err := model.ValidateMetaIdentifier(d.ClientID); err != nil {
		return d, false, err
	}
	if err := model.ValidateMetaIdentifier(d.Name); err != nil {
		return d, false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return d, false, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT INTO meta_registry(user_id,client_id,name,description) VALUES($1,$2,$3,$4) ON CONFLICT(user_id,client_id,name) DO NOTHING`, userID, d.ClientID, d.Name, d.Description)
	if err != nil {
		return d, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return d, false, err
	}
	// A separate statement sees a concurrent registration after ON CONFLICT waits.
	err = tx.QueryRow(`SELECT description FROM meta_registry WHERE user_id=$1 AND client_id=$2 AND name=$3`, userID, d.ClientID, d.Name).Scan(&d.Description)
	if err != nil {
		return d, false, err
	}
	d.QualifiedName = d.ClientID + "." + d.Name
	return d, n == 1, tx.Commit()
}

func (s *Storage) MetaRegistry(userID int64, clientID string) ([]model.MetaDefinition, error) {
	if clientID != "" {
		if err := model.ValidateMetaIdentifier(clientID); err != nil {
			return nil, err
		}
	}
	rows, err := s.db.Query(`SELECT client_id,name,description FROM meta_registry WHERE user_id=$1 AND ($2='' OR client_id=$2) ORDER BY client_id,name`, userID, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	definitions := []model.MetaDefinition{}
	for rows.Next() {
		var d model.MetaDefinition
		if err := rows.Scan(&d.ClientID, &d.Name, &d.Description); err != nil {
			return nil, err
		}
		d.QualifiedName = d.ClientID + "." + d.Name
		definitions = append(definitions, d)
	}
	return definitions, rows.Err()
}

func (s *Storage) ValidateMetaQuery(userID int64, q model.MetaQuery) error {
	fields := append([]string{}, q.Projection...)
	for _, f := range q.Filters {
		if _, err := model.MetaLikePattern(f.Pattern); err != nil {
			return err
		}
		fields = append(fields, f.Field)
	}
	if len(fields) == 0 {
		return nil
	}
	for _, field := range fields {
		if field != "*" {
			if err := model.ValidateMetaField(field); err != nil {
				return err
			}
		}
	}
	definitions, err := s.MetaRegistry(userID, "")
	if err != nil {
		return err
	}
	registered := map[string]bool{"*": true}
	for _, d := range definitions {
		registered[d.QualifiedName] = true
	}
	for _, field := range fields {
		if !registered[field] {
			return model.UnregisteredMetaField(field)
		}
	}
	return nil
}

// WriteMeta validates all definitions before touching values. The entry lock
// prevents deletion during the transaction and serializes same-entry batches.
// sql.ErrNoRows denotes an entry outside the authenticated user's collection.
func (s *Storage) WriteMeta(userID, entryID int64, clientID string, values map[string]string, remove string) error {
	if err := model.ValidateMetaIdentifier(clientID); err != nil {
		return err
	}
	names := make([]string, 0, len(values)+1)
	for name, value := range values {
		if err := model.ValidateMetaText(value); err != nil {
			return err
		}
		names = append(names, name)
	}
	if remove != "" {
		names = append(names, remove)
	}
	slices.Sort(names)
	for _, name := range names {
		if err := model.ValidateMetaIdentifier(name); err != nil {
			return err
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	if err = tx.QueryRow(`SELECT id FROM entries WHERE user_id=$1 AND id=$2 FOR UPDATE`, userID, entryID).Scan(&id); err != nil {
		return err
	}
	ids := make(map[string]int64, len(names))
	for _, name := range names {
		err = tx.QueryRow(`SELECT id FROM meta_registry WHERE user_id=$1 AND client_id=$2 AND name=$3`, userID, clientID, name).Scan(&id)
		if err == sql.ErrNoRows {
			return model.UnregisteredMetaField(clientID + "." + name)
		}
		if err != nil {
			return err
		}
		ids[name] = id
	}
	for _, name := range names {
		if name == remove {
			_, err = tx.Exec(`DELETE FROM entry_meta WHERE entry_id=$1 AND user_id=$2 AND definition_id=$3`, entryID, userID, ids[name])
		} else {
			_, err = tx.Exec(`INSERT INTO entry_meta(entry_id,user_id,definition_id,value) VALUES($1,$2,$3,$4) ON CONFLICT(entry_id,definition_id) DO UPDATE SET value=EXCLUDED.value`, entryID, userID, ids[name], values[name])
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LoadEntryMeta performs a single selective query for a whole page. Entry owners
// are included in the join even when callers supply entries from several users.
func (s *Storage) LoadEntryMeta(entries model.Entries, fields []string) error {
	if len(fields) == 0 || len(entries) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(entries))
	byID := make(map[int64]*model.Entry, len(entries))
	for _, entry := range entries {
		values := map[string]string{}
		entry.Meta = &values
		ids = append(ids, entry.ID)
		byID[entry.ID] = entry
	}
	rows, err := s.db.Query(`SELECT m.entry_id,m.user_id,r.client_id||'.'||r.name,m.value FROM entry_meta m JOIN meta_registry r ON r.id=m.definition_id AND r.user_id=m.user_id WHERE m.entry_id=ANY($1) AND ($2 OR r.client_id||'.'||r.name=ANY($3))`, pq.Array(ids), slices.Contains(fields, "*"), pq.Array(fields))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var entryID, userID int64
		var field, value string
		if err := rows.Scan(&entryID, &userID, &field, &value); err != nil {
			return err
		}
		if entry := byID[entryID]; entry != nil && entry.UserID == userID {
			(*entry.Meta)[field] = value
		}
	}
	return rows.Err()
}

// appendMetaPredicate is shared by lists, counts, navigation and bulk actions.
// Filters must have been parsed and validated before reaching this function.
func appendMetaPredicate(conditions *[]string, args *[]any, filters []model.MetaFilter) {
	if len(filters) == 0 {
		return
	}
	parts := make([]string, 0, len(filters))
	for _, f := range filters {
		pattern, err := model.MetaLikePattern(f.Pattern)
		if err != nil {
			parts = append(parts, "FALSE")
			continue
		}
		n := len(*args) + 1
		part := fmt.Sprintf(`(r.client_id||'.'||r.name=$%d AND m.value COLLATE "C" LIKE $%d ESCAPE '\'`, n, n+1)
		if f.Pattern == "*" {
			part += ` AND m.value<>''`
		}
		parts = append(parts, part+")")
		*args = append(*args, f.Field, pattern)
	}
	*conditions = append(*conditions, `EXISTS (SELECT 1 FROM entry_meta m JOIN meta_registry r ON r.id=m.definition_id AND r.user_id=m.user_id WHERE m.entry_id=e.id AND m.user_id=e.user_id AND (`+strings.Join(parts, " OR ")+`))`)
}

func (e *EntryQueryBuilder) WithMetaQuery(q model.MetaQuery) *EntryQueryBuilder {
	appendMetaPredicate(&e.conditions, &e.args, q.Filters)
	e.metaFields = q.Projection
	return e
}

func (e *entryPaginationBuilder) WithMetaFilters(filters []model.MetaFilter) *entryPaginationBuilder {
	appendMetaPredicate(&e.conditions, &e.args, filters)
	return e
}

// MarkMatchingAsRead ignores pagination and ordering, preserving all predicates.
func (e *EntryQueryBuilder) MarkMatchingAsRead() error {
	_, err := e.store.db.Exec(`UPDATE entries SET status='read',changed_at=now() WHERE status='unread' AND id IN (SELECT e.id FROM entries e JOIN feeds f ON f.id=e.feed_id JOIN categories c ON c.id=f.category_id WHERE `+e.buildCondition()+`)`, e.args...)
	return err
}

func (s *Storage) MetaPreferences(userID int64) (model.MetaPreferences, error) {
	var p model.MetaPreferences
	var data []byte
	err := s.db.QueryRow(`SELECT preferences FROM user_meta_preferences WHERE user_id=$1`, userID).Scan(&data)
	if err == sql.ErrNoRows {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(data, &p)
	return p, err
}

func (s *Storage) SaveMetaPreferences(userID int64, p model.MetaPreferences) error {
	q := model.MetaQuery{Filters: p.Filters, Projection: append(append([]string{}, p.TitleFields...), p.ContentFields...)}
	// Preferences select concrete registered fields, never all fields implicitly.
	for _, field := range q.Projection {
		if err := model.ValidateMetaField(field); err != nil {
			return err
		}
	}
	for _, f := range q.Filters {
		if err := model.ValidateMetaField(f.Field); err != nil {
			return err
		}
	}
	if err := s.ValidateMetaQuery(userID, q); err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO user_meta_preferences(user_id,preferences) VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET preferences=EXCLUDED.preferences`, userID, string(data))
	return err
}
