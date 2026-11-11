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

package sqliteutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenFileBackedWALAndPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "mizan")
	path := filepath.Join(dir, "test.db")

	info, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = info.DB.Close() })

	if !info.IsFileBacked || info.IsReadOnly || info.FSPath != path {
		t.Fatalf("unexpected OpenInfo: %+v", info)
	}

	var mode string
	if err := info.DB.QueryRow("PRAGMA journal_mode;").Scan(&mode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Errorf("journal_mode = %q, want wal", mode)
	}

	var timeout int
	if err := info.DB.QueryRow("PRAGMA busy_timeout;").Scan(&timeout); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if timeout != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", timeout)
	}

	if _, err := info.DB.Exec("CREATE TABLE items (id TEXT PRIMARY KEY); INSERT INTO items VALUES ('a');"); err != nil {
		t.Fatalf("create/insert: %v", err)
	}
	ChmodSidecars(path)

	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Errorf("dir perm = %04o, want 0700", got)
	}

	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		fi, err := os.Stat(p)
		if err != nil {
			if os.IsNotExist(err) && p != path {
				continue
			}
			t.Fatalf("stat %s: %v", p, err)
		}
		if got := fi.Mode().Perm(); got != 0o600 {
			t.Errorf("%s perm = %04o, want 0600", p, got)
		}
	}
}

func TestOpenMemoryAndReadOnlyURI(t *testing.T) {
	for _, dsn := range []string{":memory:", "file::memory:?cache=shared"} {
		info, err := Open(dsn)
		if err != nil {
			t.Fatalf("Open(%q): %v", dsn, err)
		}
		if info.IsFileBacked {
			t.Errorf("Open(%q).IsFileBacked = true, want false", dsn)
		}
		_ = info.DB.Close()
	}

	path := filepath.Join(t.TempDir(), "ro.db")
	rw, err := Open(path)
	if err != nil {
		t.Fatalf("Open rw: %v", err)
	}
	if _, err := rw.DB.Exec("CREATE TABLE items (id TEXT PRIMARY KEY); INSERT INTO items VALUES ('x');"); err != nil {
		t.Fatalf("seed rw: %v", err)
	}
	if err := rw.DB.Close(); err != nil {
		t.Fatalf("close rw: %v", err)
	}

	ro, err := Open("file:" + path + "?mode=ro")
	if err != nil {
		t.Fatalf("Open ro: %v", err)
	}
	defer func() { _ = ro.DB.Close() }()
	if !ro.IsFileBacked || !ro.IsReadOnly {
		t.Fatalf("unexpected ro OpenInfo: %+v", ro)
	}
	var id string
	if err := ro.DB.QueryRow("SELECT id FROM items;").Scan(&id); err != nil || id != "x" {
		t.Fatalf("ro query id = %q, err = %v", id, err)
	}
}
