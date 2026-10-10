// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package database

import (
	"database/sql"
	"fmt"
)

// Extension versions are independent of upstream's positional migration sequence.
const metaSchemaVersion = 1

func migrateMeta(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(7264198301)`); err != nil {
		return err
	}
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS miniflux_extensions (name text PRIMARY KEY, version integer NOT NULL)`); err != nil {
		return err
	}
	var version int
	err = tx.QueryRow(`SELECT version FROM miniflux_extensions WHERE name='meta'`).Scan(&version)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if version > metaSchemaVersion {
		return fmt.Errorf("unsupported metadata schema version %d", version)
	}
	if version == 0 {
		_, err = tx.Exec(`
		CREATE TABLE meta_registry (
		 id bigserial PRIMARY KEY,
		 user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		 client_id text NOT NULL CHECK (client_id ~ '^[a-z0-9_-]{1,64}$'),
		 name text NOT NULL CHECK (name ~ '^[a-z0-9_-]{1,64}$'),
		 description text NOT NULL,
		 UNIQUE(user_id, client_id, name),
		 UNIQUE(user_id, id)
		);
		CREATE TABLE entry_meta (
		 entry_id bigint NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
		 user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		 definition_id bigint NOT NULL,
		 value text NOT NULL,
		 PRIMARY KEY(entry_id, definition_id),
		 FOREIGN KEY(user_id, definition_id) REFERENCES meta_registry(user_id, id) ON DELETE CASCADE
		);
		CREATE INDEX entry_meta_definition_idx ON entry_meta(definition_id, entry_id);
		CREATE INDEX entry_meta_user_idx ON entry_meta(user_id);
		CREATE TABLE user_meta_preferences (
		 user_id bigint PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		 preferences jsonb NOT NULL DEFAULT '{}'::jsonb
		);
		INSERT INTO miniflux_extensions(name, version) VALUES ('meta', 1);
		`)
		if err != nil {
			return fmt.Errorf("metadata migration: %w", err)
		}
	}
	return tx.Commit()
}

func checkMetaSchema(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`SELECT version FROM miniflux_extensions WHERE name='meta'`).Scan(&version); err != nil {
		return fmt.Errorf("metadata schema is not initialized; run migrations: %w", err)
	}
	if version != metaSchemaVersion {
		return fmt.Errorf("metadata schema version %d, expected %d", version, metaSchemaVersion)
	}
	return nil
}
