// Copyright (c) 2025 Sidero Labs, Inc.
//
// Use of this software is governed by the Business Source License
// included in the LICENSE file.

package siderolink

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/go-multierror"
	"github.com/siderolabs/go-circular/zstd"
	"go.uber.org/zap"

	"github.com/siderolabs/omni/internal/pkg/config"
	"github.com/siderolabs/omni/internal/pkg/siderolink/logstore"
)

type logStoreManager struct {
	config     *config.LogsMachine
	logger     *zap.Logger
	compressor *zstd.Compressor
	db         *sql.DB
}

func (c *logStoreManager) Exists(ctx context.Context, id MachineID) (bool, error) {
	if c.config.Storage.Enabled {
		matches, err := c.logFiles(id)
		if err != nil {
			return false, fmt.Errorf("failed to list log files for machine %q: %w", id, err)
		}

		if len(matches) > 0 {
			return true, nil
		}
	}

	if c.config.SQLiteStorage.Enabled {
		exists, err := logstore.SQLiteTableExists(ctx, c.db, string(id), c.config.SQLiteStorage.Timeout)
		if err != nil {
			return false, fmt.Errorf("failed to check sqlite log store existence for machine %q: %w", id, err)
		}

		return exists, nil
	}

	return false, nil
}

func (c *logStoreManager) Remove(ctx context.Context, id MachineID) error {
	var errs error

	if c.config.Storage.Enabled {
		matches, err := c.logFiles(id)
		if err != nil {
			return fmt.Errorf("failed to list log files for machine %q: %w", id, err)
		}

		for _, match := range matches {
			if err = os.Remove(match); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = multierror.Append(errs, err)
			}
		}
	}

	if c.config.SQLiteStorage.Enabled {
		if err := logstore.DropSQLiteTable(ctx, c.db, string(id), c.config.SQLiteStorage.Timeout); err != nil {
			errs = multierror.Append(errs, fmt.Errorf("failed to drop sqlite log store for machine %q: %w", id, err))
		}
	}

	return errs
}

// logFiles returns all log files for the given machine ID.
//
// It probes the file system to check if a log file exists for this machine.
// Checks both for the old (/path/machine-id.log) and the new (/path/machine-id.log.NUM) format.
func (c *logStoreManager) logFiles(id MachineID) ([]string, error) {
	return filepath.Glob(filepath.Join(c.config.Storage.Path, string(id)+".log*"))
}

func (c *logStoreManager) Create(ctx context.Context, id MachineID) (*logstore.LogStore, error) {
	return logstore.NewLogStore(ctx, c.config, c.db, string(id), c.compressor, c.logger)
}

func newLogStoreManager(config *config.LogsMachine, compressor *zstd.Compressor, logger *zap.Logger) (*logStoreManager, error) {
	var db *sql.DB

	if config.SQLiteStorage.Enabled {
		dir := filepath.Dir(config.SQLiteStorage.Path)

		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create directory for sqlite database %q: %w", dir, err)
		}

		dsn := "file:" + config.SQLiteStorage.Path + "?" + config.SQLiteStorage.Options

		logger.Info("opening sqlite database", zap.String("conn_string", dsn))

		var err error

		if db, err = sql.Open("sqlite", dsn); err != nil {
			return nil, fmt.Errorf("failed to open sqlite database %q: %w", dsn, err)
		}
	}

	return &logStoreManager{
		db:         db,
		config:     config,
		compressor: compressor,
		logger:     logger,
	}, nil
}

func (c *logStoreManager) Close() error {
	return c.db.Close()
}
