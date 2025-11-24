// Copyright (c) 2025 Sidero Labs, Inc.
//
// Use of this software is governed by the Business Source License
// included in the LICENSE file.

package logstore

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/siderolabs/omni/internal/pkg/config"
)

const (
	readBatchSize = 1024
	schemaSQL     = `
CREATE TABLE IF NOT EXISTS "%[1]s" (
  id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  created_at INTEGER NOT NULL,
  message TEXT NOT NULL
) STRICT;
`
)

func SQLiteTableExists(ctx context.Context, db *sql.DB, id string, timeout time.Duration) (bool, error) {
	tableName := buildTableName(id)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var count int

	query := "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?"
	if err := db.QueryRowContext(ctx, query, tableName).Scan(&count); err != nil {
		return false, err
	}

	return count > 0, nil
}

func DropSQLiteTable(ctx context.Context, db *sql.DB, id string, timeout time.Duration) error {
	tableName := buildTableName(id)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	_, err := db.ExecContext(ctx, fmt.Sprintf(`DROP TABLE %s`, tableName))
	// _, err := db.ExecContext(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tableName))
	if err != nil {
		return fmt.Errorf("failed to drop sqlite log table %q: %w", tableName, err)
	}

	return nil
}

func NewSQLiteStorage(ctx context.Context, db *sql.DB, id string, config config.LogsMachineSQLiteStorage, logger *zap.Logger) (*SQLiteStorage, error) {
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()

	tableName := buildTableName(id)

	logger.Debug("create sqlite storage", zap.String("table_name", tableName), zap.String("machine_id", id))

	schemaReplaced := fmt.Sprintf(schemaSQL, tableName)

	if _, err := db.ExecContext(ctx, schemaReplaced); err != nil {
		return nil, fmt.Errorf("applying schema migration: %w", err)
	}

	return &SQLiteStorage{
		db:        db,
		config:    config,
		logger:    logger,
		tableName: tableName,
		lastFlush: time.Now(),
		buffer:    make([]bufferedLog, 0, 1024),
	}, nil
}

type SQLiteStorage struct {
	lastFlush       time.Time
	db              *sql.DB
	logger          *zap.Logger
	tableName       string
	buffer          []bufferedLog
	config          config.LogsMachineSQLiteStorage
	bufferSizeBytes uint64
}

type HandleMessageFunc func(message []byte) error

func (s *SQLiteStorage) Load(ctx context.Context, f HandleMessageFunc) error {
	var lastID int64

	for {
		newLastID, count, err := s.loadBatch(ctx, f, lastID)
		if err != nil {
			return err
		}

		lastID = newLastID

		// If we retrieved fewer rows than the batch size, we have reached the end.
		if count < readBatchSize {
			break
		}
	}

	return nil
}

func (s *SQLiteStorage) loadBatch(ctx context.Context, f HandleMessageFunc, lastID int64) (int64, int, error) {
	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()

	query := fmt.Sprintf(`SELECT id, message FROM %s WHERE id > ? ORDER BY id ASC LIMIT %d`, s.tableName, readBatchSize)

	rows, err := s.db.QueryContext(ctx, query, lastID)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to query log batch: %w", err)
	}

	defer rows.Close() //nolint:errcheck

	var (
		count     int
		currentID = lastID
	)

	for rows.Next() {
		var line []byte

		if err = rows.Scan(&currentID, &line); err != nil {
			return 0, 0, fmt.Errorf("failed to scan log row: %w", err)
		}

		if err = f(line); err != nil {
			return 0, 0, fmt.Errorf("failed to handle log line: %w", err)
		}

		count++
	}

	if err = rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("failed to iterate over log rows: %w", err)
	}

	return currentID, count, nil
}

func (s *SQLiteStorage) WriteLine(ctx context.Context, line []byte) error {
	s.buffer = append(s.buffer, bufferedLog{
		createdAt: time.Now().Unix(),
		message:   line,
	})

	// Flush to DB if needed.
	if s.bufferSizeBytes >= s.config.WriteBufferMaxCapacity || time.Since(s.lastFlush) > s.config.FlushPeriod {
		return s.Flush(ctx)
	}

	return nil
}

// Flush writes all buffered lines to the database in a single transaction.
func (s *SQLiteStorage) Flush(ctx context.Context) error {
	if len(s.buffer) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("error starting flush transaction: %w", err)
	}

	defer tx.Rollback() //nolint:errcheck

	query := fmt.Sprintf(`INSERT INTO %s (created_at, message) VALUES (?, ?)`, s.tableName)

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return fmt.Errorf("error preparing flush statement: %w", err)
	}

	defer stmt.Close() //nolint:errcheck

	for _, entry := range s.buffer {
		if _, err = stmt.ExecContext(ctx, entry.createdAt, string(entry.message)); err != nil {
			return fmt.Errorf("failed to insert buffered log: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit flush transaction: %w", err)
	}

	s.buffer = s.buffer[:0]
	s.bufferSizeBytes = 0
	s.lastFlush = time.Now()

	return nil
}

type bufferedLog struct {
	message   []byte
	createdAt int64
}

// buildTableName generates a table name based on the given id using its MD5 hash.
//
// This ensures that the table name is safe against SQL injection and length constraints.
func buildTableName(id string) string {
	hash := md5.Sum([]byte(id))

	return "logs_" + hex.EncodeToString(hash[:])
}
