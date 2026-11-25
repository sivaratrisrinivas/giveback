package ledger

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// A ledger made before the amount check was stored gets the new columns.
func TestMigrateOldLedger(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE pages (id INTEGER PRIMARY KEY, photo_hash TEXT NOT NULL UNIQUE, function TEXT NOT NULL, date TEXT NOT NULL,
		rotation INTEGER NOT NULL, read1 TEXT NOT NULL, read2 TEXT NOT NULL, model1 TEXT NOT NULL, model2 TEXT NOT NULL, added_at TEXT NOT NULL);
	CREATE TABLE lines (id INTEGER PRIMARY KEY, page_id INTEGER NOT NULL, idx INTEGER NOT NULL, number TEXT NOT NULL, text TEXT NOT NULL,
		name TEXT NOT NULL, place TEXT NOT NULL, amount_digits TEXT NOT NULL, gold REAL NOT NULL, item TEXT NOT NULL, flags TEXT NOT NULL, crop TEXT NOT NULL);
	INSERT INTO pages VALUES (1, 'h', 'f', '12-2-1994', 0, '{}', '{}', 'm', 'm', 'now');
	INSERT INTO lines VALUES (1, 1, 0, '5', 't', 'n', '', '1116', 0, '', '["amount read as ₹1,116, then as ₹116"]', '');`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ConfirmGift(1, "1116", 0); err != nil {
		t.Fatal(err)
	}
	e, ok, err := s.Entry(1)
	if err != nil || !ok || !e.Checked || len(e.Flags) != 0 || e.AmountDigits != "1116" {
		t.Fatalf("entry after migrate + confirm: %+v %v %v", e, ok, err)
	}
	s.Close()
	if s, err = Open(dir); err != nil { // opening again is a no-op
		t.Fatal(err)
	}
}
