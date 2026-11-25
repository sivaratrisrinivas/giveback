package ledger

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/sivaratrisrinivas/giveback/internal/vision"
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
	CREATE TABLE updates (update_id INTEGER PRIMARY KEY);
	INSERT INTO updates VALUES (41);
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
	if again, err := s.Claim(41); err != nil || again { // handled by the old version
		t.Fatalf("old update claimed again: %v %v", again, err)
	}
	s.Close()
	if s, err = Open(dir); err != nil { // opening again is a no-op
		t.Fatal(err)
	}
}

// An update is handled once; a try that never finished gets one more go.
func TestClaimFinish(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	claim := func(id int64) bool {
		t.Helper()
		ok, err := s.Claim(id)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !claim(1) {
		t.Fatal("new update not claimed")
	}
	s.Finish(1)
	if claim(1) {
		t.Error("finished update claimed again")
	}
	// Update 2 starts, then the process dies before Finish.
	if !claim(2) || !claim(2) {
		t.Error("an unfinished update should get a second try")
	}
	if claim(2) {
		t.Error("an update that keeps failing must not be tried forever")
	}
}

func TestUsage(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.RecordCalls(5, []vision.Call{
		{Model: "gemini-4-hydrogen", Err: "gemini HTTP 503", Elapsed: time.Second},
		{Model: "claude-haiku-5-5", OK: true, Elapsed: 4 * time.Second, In: 2000, Out: 400, CostUSD: 0.0004},
		{Model: "claude-haiku-5-5", OK: true, Elapsed: 6 * time.Second, In: 1000, Out: 100, CostUSD: 0.00015},
	})
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.Usage(time.Now().Add(-time.Hour))
	if err != nil || len(u) != 2 {
		t.Fatalf("usage = %+v, %v", u, err)
	}
	h, g := u[0], u[1]
	if h.Model != "claude-haiku-5-5" || h.Calls != 2 || h.In != 3000 || h.Out != 500 || h.AvgSecs != 5 || h.CostUSD < 0.00054 || h.CostUSD > 0.00056 {
		t.Errorf("haiku = %+v", h)
	}
	if g.Calls != 1 || g.Failed != 1 {
		t.Errorf("gemini = %+v", g)
	}
	if later, _ := s.Usage(time.Now().Add(time.Hour)); len(later) != 0 {
		t.Errorf("usage since the future = %+v", later)
	}
}
