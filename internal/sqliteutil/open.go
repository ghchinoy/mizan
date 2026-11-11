// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package sqliteutil centralizes SQLite connection initialization, busy-timeout
// configuration, WAL enablement, and 0600 file/sidecar permission hygiene
// shared by internal/registry/sqlite and internal/results/sqlite.
package sqliteutil

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	// Registers the pure-Go "sqlite" driver with database/sql.
	_ "modernc.org/sqlite"
)

// OpenInfo describes the opened SQLite handle and any backing filesystem path.
type OpenInfo struct {
	DB           *sql.DB
	FSPath       string
	IsFileBacked bool
	IsReadOnly   bool
}

// Open opens a SQLite handle at path with SetMaxOpenConns(1), a 5-second
// busy_timeout, owner-only 0700/0600 directory and file permissions, and WAL
// journal mode when file-backed and writable.
func Open(path string) (OpenInfo, error) {
	fsPath, isFileBacked, isReadOnly := parseSQLitePath(path)
	if isFileBacked && !isReadOnly {
		if dir := filepath.Dir(fsPath); dir != "" {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return OpenInfo{}, fmt.Errorf("sqlite: create db dir: %w", err)
			}
		}
		f, err := os.OpenFile(fsPath, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return OpenInfo{}, fmt.Errorf("sqlite: open db file: %w", err)
		}
		_ = f.Close()
		if err := os.Chmod(fsPath, 0o600); err != nil {
			return OpenInfo{}, fmt.Errorf("sqlite: chmod db file: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return OpenInfo{}, fmt.Errorf("sqlite: open: %w", err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec("PRAGMA busy_timeout = 5000;"); err != nil {
		_ = db.Close()
		return OpenInfo{}, fmt.Errorf("sqlite: set busy_timeout: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return OpenInfo{}, fmt.Errorf("sqlite: ping: %w", err)
	}

	if isFileBacked && !isReadOnly {
		if err := os.Chmod(fsPath, 0o600); err != nil {
			_ = db.Close()
			return OpenInfo{}, fmt.Errorf("sqlite: chmod db file: %w", err)
		}
		var mode string
		if err := db.QueryRow("PRAGMA journal_mode;").Scan(&mode); err == nil && !strings.EqualFold(mode, "wal") {
			var nextMode string
			if err := db.QueryRow("PRAGMA journal_mode = WAL;").Scan(&nextMode); err == nil && strings.EqualFold(nextMode, "wal") {
				_, _ = db.Exec("PRAGMA synchronous = NORMAL;")
			}
		}
		ChmodSidecars(fsPath)
	}

	return OpenInfo{
		DB:           db,
		FSPath:       fsPath,
		IsFileBacked: isFileBacked,
		IsReadOnly:   isReadOnly,
	}, nil
}

// ChmodSidecars applies mode 0600 to existing SQLite WAL and SHM sidecar files,
// ignoring missing sidecars.
func ChmodSidecars(fsPath string) {
	if fsPath == "" {
		return
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Chmod(fsPath+suffix, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			continue
		}
	}
}

func parseSQLitePath(path string) (fsPath string, isFileBacked bool, isReadOnly bool) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" || trimmed == ":memory:" || strings.HasPrefix(trimmed, "file::memory:") || strings.Contains(trimmed, "mode=memory") {
		return "", false, false
	}
	if after, ok := strings.CutPrefix(trimmed, "file:"); ok {
		rawPath, query, _ := strings.Cut(after, "?")
		if rawPath == "" || rawPath == ":memory:" {
			return "", false, false
		}
		return rawPath, true, strings.Contains(query, "mode=ro")
	}
	rawPath, query, hasQuery := strings.Cut(trimmed, "?")
	if hasQuery {
		return rawPath, true, strings.Contains(query, "mode=ro")
	}
	return trimmed, true, false
}
