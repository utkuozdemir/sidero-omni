// Copyright (c) 2025 Sidero Labs, Inc.
//
// Use of this software is governed by the Business Source License
// included in the LICENSE file.

package logstore

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/siderolabs/go-circular"
	"github.com/siderolabs/go-tail"
	"go.uber.org/zap"

	"github.com/siderolabs/omni/internal/pkg/config"
)

func NewLogStore(ctx context.Context, config *config.LogsMachine, db *sql.DB, id string, compressor circular.Compressor, logger *zap.Logger) (*LogStore, error) {
	bufferOpts := []circular.OptionFunc{
		circular.WithInitialCapacity(config.BufferInitialCapacity),
		circular.WithMaxCapacity(config.BufferMaxCapacity),
		circular.WithSafetyGap(config.BufferSafetyGap),
		circular.WithNumCompressedChunks(config.Storage.NumCompressedChunks, compressor),
		circular.WithLogger(logger),
	}

	var sqLiteStorage *SQLiteStorage

	switch {
	case db != nil:
		var err error

		if sqLiteStorage, err = NewSQLiteStorage(ctx, db, id, config.SQLiteStorage, logger); err != nil {
			return nil, fmt.Errorf("failed to create sqlite storage for machine %q: %w", id, err)
		}
	case config.Storage.Enabled:
		bufferOpts = append(bufferOpts, circular.WithPersistence(circular.PersistenceOptions{
			ChunkPath:     filepath.Join(config.Storage.Path, id+".log"),
			FlushInterval: config.Storage.FlushPeriod,
			FlushJitter:   config.Storage.FlushJitter,
		}))
	}

	buffer, err := circular.NewBuffer(bufferOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create circular buffer for machine %q: %w", id, err)
	}

	switch {
	case sqLiteStorage != nil:
		if err = sqLiteStorage.Load(ctx, func(msg []byte) error {
			return writeLine(buffer, msg)
		}); err != nil {
			return nil, err
		}
	case config.Storage.Enabled:
		loadLegacyLogs(config, id, buffer, logger)
	}

	return &LogStore{
		sqliteStorage: sqLiteStorage,
		buf:           buffer,
		logger:        logger,
	}, nil
}

type LogStore struct {
	buf           *circular.Buffer
	sqliteStorage *SQLiteStorage
	logger        *zap.Logger
}

func (s *LogStore) WriteLine(ctx context.Context, message []byte) error {
	if err := writeLine(s.buf, message); err != nil {
		return err
	}

	if s.sqliteStorage != nil {
		if err := s.sqliteStorage.WriteLine(ctx, message); err != nil {
			return fmt.Errorf("failed to write line to sqlite storage: %w", err)
		}
	}

	return nil
}

func writeLine(w io.Writer, message []byte) error {
	if _, err := w.Write([]byte("\n")); err != nil {
		return err
	}

	if _, err := w.Write(message); err != nil {
		return err
	}

	if _, err := w.Write([]byte("\n")); err != nil {
		return err
	}

	return nil
}

func (s *LogStore) Close(ctx context.Context) error {
	bufErr := s.buf.Close()

	if s.sqliteStorage == nil {
		return bufErr
	}

	return s.sqliteStorage.Flush(ctx)
}

func (s *LogStore) Reader(nLines int, follow bool) (*LineReader, error) {
	var rdr io.ReadSeekCloser

	if follow {
		rdr = s.buf.GetStreamingReader()
	} else {
		rdr = s.buf.GetReader()
	}

	if rdr == nil {
		return nil, fmt.Errorf("buffer reader is not available")
	}

	if nLines > 0 {
		// since we are surrounding each message with \n we should increase lines by two times.
		seekLines := nLines * 2

		if err := tail.SeekLines(rdr, seekLines); err != nil {
			return nil, fmt.Errorf("failed to seek %d lines: %w", seekLines, err)
		}
	}

	return &LineReader{reader: rdr}, nil
}

// LineReader is a reader which reads lines surrounded by \n from the underlying reader.
type LineReader struct {
	buf    *bufio.Reader
	reader io.ReadCloser
}

// Close closes the LineReader underlying reader.
func (r *LineReader) Close() error {
	return r.reader.Close()
}

// ReadLine reads a line from the underlying reader.
func (r *LineReader) ReadLine(context.Context) ([]byte, error) {
	if r.buf == nil {
		r.buf = bufio.NewReader(r.reader)
	}

	for {
		emptyLine, err := r.buf.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil, io.EOF
			}

			return nil, fmt.Errorf("failed to read line: %w", err)
		}

		if len(emptyLine) != 1 {
			// missed the start of the line, skipping to the next entry
			continue
		}

		logLine, err := r.buf.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil, io.EOF
			}

			return nil, fmt.Errorf("failed to read line: %w", err)
		}

		return trimNewlines(logLine), nil
	}
}

// trimNewlines trims a newline from the start and from end of a byte slice.
func trimNewlines(data []byte) []byte {
	if len(data) == 0 {
		return data
	}

	if data[0] == '\n' {
		data = data[1:]
	}

	if len(data) > 0 && data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
	}

	return data
}

// loadLegacyLogs loads logs stored of the machine with the given id in the old format, if exists, into the given writer.
// It is used to migrate logs from the old format to the new format.
// It removes the old log file and its hash file regardless of the result.
//
// It is a best-effort function and does not return any error.
func loadLegacyLogs(config *config.LogsMachine, id string, writer io.Writer, logger *zap.Logger) {
	filePath := filepath.Join(config.Storage.Path, fmt.Sprintf("%s.log", id))
	shaSumPath := filePath + ".sha256sum"

	defer func() {
		if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Error("failed to remove legacy log file", zap.String("path", filePath), zap.Error(err))
		}

		if err := os.Remove(shaSumPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Error("failed to remove legacy log hash file", zap.String("path", shaSumPath), zap.Error(err))
		}
	}()

	bufferData, err := os.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return
		}

		logger.Error("failed to read legacy log buffer file", zap.String("path", filePath), zap.Error(err))

		return
	}

	hashHexExpectedBytes, err := os.ReadFile(shaSumPath)
	if err != nil {
		logger.Error("failed to read legacy log buffer hash file", zap.String("path", shaSumPath), zap.Error(err))

		return
	}

	hashHexExpected := string(hashHexExpectedBytes)

	// verify the hash
	hashActual := sha256.Sum256(bufferData)
	hashHexActual := hex.EncodeToString(hashActual[:])

	if hashHexExpected != hashHexActual {
		logger.Error("invalid legacy log buffer hash in file", zap.String("expected", hashHexExpected), zap.String("actual", hashHexActual))

		return
	}

	if _, err = io.Copy(writer, bytes.NewReader(bufferData)); err != nil {
		logger.Error("failed to write legacy log buffer to writer", zap.Error(err))
	}

	logger.Info("loaded legacy log buffer", zap.String("path", filePath))
}
