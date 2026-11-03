package sqlitestore

import (
	"path/filepath"
	"testing"
)

// A database file from a cloned repository is untrusted: views and triggers in
// it must not be allowed to call unsafe functions.
func TestDatabasesDoNotTrustTheirSchema(t *testing.T) {
	for _, extra := range []string{"", "mode=ro&"} {
		path := filepath.Join(t.TempDir(), "x.db")
		first, err := openDB(path, "") // create the file so a read-only open can follow
		if err != nil {
			t.Fatal(err)
		}
		if _, err := first.Exec("PRAGMA user_version = 1"); err != nil {
			t.Fatal(err)
		}
		first.Close()
		db, err := openDB(path, extra)
		if err != nil {
			t.Fatal(err)
		}
		var trusted int
		if err := db.QueryRow("PRAGMA trusted_schema").Scan(&trusted); err != nil || trusted != 0 {
			t.Errorf("%q: trusted_schema = %d, err %v; want 0", extra, trusted, err)
		}
		db.Close()
	}
}
