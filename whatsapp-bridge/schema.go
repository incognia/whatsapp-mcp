package main

import (
	"database/sql"
	"fmt"
)

// schemaMigrations are applied in order on top of the base chats/messages tables created by
// openMessageStore. Each step runs in its own transaction and bumps PRAGMA user_version.
// Only additive DDL is allowed, so an older binary keeps working against a newer database.
var schemaMigrations = []string{
	// 1: message listeners and their delivery log
	`
	CREATE TABLE IF NOT EXISTS listeners (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		match_mode TEXT NOT NULL DEFAULT 'or' CHECK (match_mode IN ('or', 'and')),
		chat_jids TEXT NOT NULL DEFAULT '[]',
		senders TEXT NOT NULL DEFAULT '[]',
		contains TEXT NOT NULL DEFAULT '[]',
		regex TEXT NOT NULL DEFAULT '',
		mentions_me INTEGER NOT NULL DEFAULT 0,
		include_from_me INTEGER NOT NULL DEFAULT 0,
		webhook_url TEXT NOT NULL,
		secret TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS listener_deliveries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		listener_id INTEGER NOT NULL REFERENCES listeners(id) ON DELETE CASCADE,
		delivery_id TEXT NOT NULL,
		event TEXT NOT NULL,
		message_id TEXT,
		chat_jid TEXT,
		status TEXT NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0,
		status_code INTEGER,
		error TEXT,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		finished_at TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_listener_deliveries_listener ON listener_deliveries(listener_id, id DESC);
	`,
}

// migrate brings the database up to the latest schema version
func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("failed to read schema version: %v", err)
	}
	for v := version; v < len(schemaMigrations); v++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(schemaMigrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("schema migration %d failed: %v", v+1, err)
		}
		// PRAGMA does not accept bound parameters; v+1 is an integer we control
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to set schema version %d: %v", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
