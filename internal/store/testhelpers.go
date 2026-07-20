package store

import (
	"path/filepath"
	"runtime"
)

// MigrationsDirForTests returns the absolute path to deploy/base/migrations,
// resolved relative to this source file so it works no matter which
// package's test binary calls it. Tests that need a migrated database call
// Open then Migrate(store.MigrationsDirForTests()).
func MigrationsDirForTests() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "deploy", "base", "migrations")
}
