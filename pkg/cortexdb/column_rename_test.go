package cortexdb

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// renameColumn renames a column for a test that wants it unreadable, and
// returns the rename back. Both run on one connection, pinned out of the
// pool until the rename back, which the test's cleanup makes if the test
// did not.
//
// SQLite parses ALTER TABLE ... RENAME COLUMN against the schema its
// connection has cached, and when the column is not there it answers
// `no such column: "x"` at once, where any other statement would find the
// schema changed, reload it and run. A pooled connection that last read the
// schema before the first rename still holds the old names, so the rename
// back failed whenever the pool handed it out instead of the connection
// that renamed: which one it hands out depends on what else borrowed one in
// between, the inference maintainer's background step for one, which is
// why TestUnderACallerScopeAnUnreadableMemoryIsHidden failed now and then
// under -race. The connection that made the first rename holds the new
// schema, so the rename back on it always finds the column.
func renameColumn(t *testing.T, db *DB, table, from, to string) (restore func()) {
	t.Helper()
	ctx := context.Background()
	conn, err := db.SQL().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(table, from, to string) error {
		_, err := conn.ExecContext(ctx, `ALTER TABLE `+table+` RENAME COLUMN `+from+` TO `+to)
		return err
	}
	if err := exec(table, from, to); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	var once sync.Once
	var restoreErr error
	back := func() error {
		once.Do(func() {
			restoreErr = errors.Join(exec(table, to, from), conn.Close())
		})
		return restoreErr
	}
	t.Cleanup(func() {
		if err := back(); err != nil {
			t.Errorf("rename %s.%s back to %s: %v", table, to, from, err)
		}
	})
	return func() {
		t.Helper()
		if err := back(); err != nil {
			t.Fatalf("rename %s.%s back to %s: %v", table, to, from, err)
		}
	}
}
