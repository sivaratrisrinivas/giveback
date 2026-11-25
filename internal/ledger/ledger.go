// Package ledger keeps a family's gift notebook on disk: one SQLite file for
// the pages and lines, and a JPEG of every handwritten line beside it, so a
// reply can always show the original line as proof.
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure Go, so the binary still builds with CGO_ENABLED=0

	"github.com/sivaratrisrinivas/giveback/internal/notebook"
	"github.com/sivaratrisrinivas/giveback/internal/vision"
)

type Store struct {
	db  *sql.DB
	dir string
}

const schema = `
CREATE TABLE IF NOT EXISTS pages (
	id         INTEGER PRIMARY KEY,
	photo_hash TEXT NOT NULL UNIQUE, -- SHA-256 of the photo: never read the same photo twice
	function   TEXT NOT NULL,
	date       TEXT NOT NULL,
	rotation   INTEGER NOT NULL,
	read1      TEXT NOT NULL,        -- the model's raw answers, kept for replay and audit
	read2      TEXT NOT NULL,
	model1     TEXT NOT NULL,
	model2     TEXT NOT NULL,
	added_at   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS lines (
	id            INTEGER PRIMARY KEY,
	page_id       INTEGER NOT NULL REFERENCES pages(id),
	idx           INTEGER NOT NULL, -- order on the page
	number        TEXT NOT NULL,
	text          TEXT NOT NULL,
	name          TEXT NOT NULL,
	place         TEXT NOT NULL,
	amount_digits TEXT NOT NULL,
	gold          REAL NOT NULL,
	item          TEXT NOT NULL,
	flags         TEXT NOT NULL,    -- JSON list of reasons a person should check this line
	crop          TEXT NOT NULL,    -- path of the line's picture, relative to the data dir; "" if none
	second_digits TEXT NOT NULL DEFAULT '', -- the amount check's reading (read 2)
	second_gold   REAL NOT NULL DEFAULT 0,
	checked       INTEGER NOT NULL DEFAULT 0 -- 1 once the family confirmed the gift in chat
);
CREATE TABLE IF NOT EXISTS chats (
	chat_id INTEGER PRIMARY KEY,
	mode    TEXT NOT NULL,          -- "pages" while the family is adding notebook pages
	set_at  TEXT NOT NULL DEFAULT '' -- UTC RFC 3339: when the mode was last set or refreshed
);
CREATE TABLE IF NOT EXISTS updates (
	update_id INTEGER PRIMARY KEY,  -- Telegram updates seen (it re-delivers on slow or failed replies)
	done      INTEGER NOT NULL DEFAULT 1, -- 0 while being handled; stays 0 if the process died mid-way
	attempts  INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS calls (
	id          INTEGER PRIMARY KEY,
	at          TEXT NOT NULL,       -- UTC, RFC 3339
	update_id   INTEGER NOT NULL,    -- the Telegram update that caused it, 0 if none
	model       TEXT NOT NULL,
	ok          INTEGER NOT NULL,
	error       TEXT NOT NULL,
	elapsed_ms  INTEGER NOT NULL,
	in_tokens   INTEGER NOT NULL,
	out_tokens  INTEGER NOT NULL,
	thinking    INTEGER NOT NULL,
	cost_usd    REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS family (
	relation TEXT PRIMARY KEY,      -- "father", "mother", or a Telugu relation word (బాబాయి)
	name     TEXT NOT NULL          -- who it is, surname first
);
CREATE TABLE IF NOT EXISTS asks (
	id          INTEGER PRIMARY KEY, -- goes in the Yes/No buttons
	line_id     INTEGER NOT NULL,
	person      TEXT NOT NULL,       -- the name we asked about, as the card wrote it
	relation    TEXT NOT NULL,       -- the relation word on the line, "" if none
	asked_at    TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS answers (
	person_key  TEXT NOT NULL,       -- match.PersonKey: the same person whatever the script or order
	line_id     INTEGER NOT NULL,
	yes         INTEGER NOT NULL,
	answered_at TEXT NOT NULL,
	PRIMARY KEY (person_key, line_id)
);`

// Open opens (or creates) the ledger in dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "crops"), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ledger.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one writer; the bot handles one message at a time anyway
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db, dir: dir}, nil
}

// migrate adds columns that ledgers made by older versions lack.
func migrate(db *sql.DB) error {
	for _, c := range []struct{ table, name, def string }{
		{"lines", "second_digits", "TEXT NOT NULL DEFAULT ''"},
		{"lines", "second_gold", "REAL NOT NULL DEFAULT 0"},
		{"lines", "checked", "INTEGER NOT NULL DEFAULT 0"},
		{"updates", "done", "INTEGER NOT NULL DEFAULT 1"}, // updates seen before were handled
		{"updates", "attempts", "INTEGER NOT NULL DEFAULT 1"},
		{"chats", "set_at", "TEXT NOT NULL DEFAULT ''"}, // unknown: such a mode counts as old
	} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, c.table, c.name).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := db.Exec(`ALTER TABLE ` + c.table + ` ADD COLUMN ` + c.name + ` ` + c.def); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// Dir is where the ledger lives; crop paths are relative to it.
func (s *Store) Dir() string { return s.dir }

// PageInfo describes a stored page.
type PageInfo struct {
	ID       int64
	Model    string // who did read 1
	Function string
	Date     string
	Lines    int
	Flagged  int
}

// PageByHash finds a page already read from this photo.
func (s *Store) PageByHash(hash string) (PageInfo, bool, error) {
	var p PageInfo
	err := s.db.QueryRow(`SELECT id, function, date, model1 FROM pages WHERE photo_hash = ?`, hash).Scan(&p.ID, &p.Function, &p.Date, &p.Model)
	if errors.Is(err, sql.ErrNoRows) {
		return PageInfo{}, false, nil
	}
	if err != nil {
		return PageInfo{}, false, err
	}
	err = s.db.QueryRow(`SELECT count(*), coalesce(sum(flags != '[]'), 0) FROM lines WHERE page_id = ?`, p.ID).Scan(&p.Lines, &p.Flagged)
	return p, true, err
}

// AddPage stores a page read from the photo with this hash, writing a JPEG of
// every line that has a crop.
func (s *Store) AddPage(hash string, p notebook.Page) (PageInfo, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return PageInfo{}, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO pages (photo_hash, function, date, rotation, read1, read2, model1, model2, added_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		hash, p.Function, p.Date, p.Rotation, string(p.FirstRead.JSON), string(p.SecondRead.JSON),
		p.FirstRead.Model, p.SecondRead.Model, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return PageInfo{}, fmt.Errorf("insert page: %w", err)
	}
	pageID, _ := res.LastInsertId()
	info := PageInfo{ID: pageID, Function: p.Function, Date: p.Date, Lines: len(p.Lines)}

	cropDir := filepath.Join("crops", fmt.Sprint(pageID))
	if err := os.MkdirAll(filepath.Join(s.dir, cropDir), 0o755); err != nil {
		return PageInfo{}, err
	}
	for i, l := range p.Lines {
		crop := ""
		if l.Crop != nil {
			b, err := notebook.EncodeJPEG(l.Crop)
			if err != nil {
				return PageInfo{}, err
			}
			crop = filepath.Join(cropDir, fmt.Sprintf("%02d.jpg", i+1))
			if err := os.WriteFile(filepath.Join(s.dir, crop), b, 0o644); err != nil {
				return PageInfo{}, err
			}
		}
		flags := l.Flags
		if flags == nil {
			flags = []string{}
		}
		fj, _ := json.Marshal(flags)
		if len(flags) > 0 {
			info.Flagged++
		}
		if _, err := tx.Exec(`INSERT INTO lines (page_id, idx, number, text, name, place, amount_digits, gold, item, flags, crop, second_digits, second_gold)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			pageID, i, l.Number, l.Text, l.Name, l.Place, l.AmountDigits, l.GoldSovereigns, l.Item, string(fj), crop,
			l.SecondDigits, l.SecondGold); err != nil {
			return PageInfo{}, fmt.Errorf("insert line: %w", err)
		}
	}
	return info, tx.Commit()
}

// DeletePage removes a page, its lines and their pictures, so the photo can
// be read again (e.g. it was read by the fallback model).
func (s *Store) DeletePage(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Answers about these lines go too: the new reading gives new line IDs.
	for _, q := range []string{
		`DELETE FROM answers WHERE line_id IN (SELECT id FROM lines WHERE page_id = ?)`,
		`DELETE FROM asks WHERE line_id IN (SELECT id FROM lines WHERE page_id = ?)`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM lines WHERE page_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM pages WHERE id = ?`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(s.dir, "crops", fmt.Sprint(id)))
}

// Entry is one notebook line with the page it came from.
type Entry struct {
	ID           int64
	Function     string // the page heading: which function the gift was given at
	Date         string // as written on the page
	Number       string
	Text         string
	Name         string
	Place        string
	AmountDigits string
	Gold         float64
	Item         string
	Flags        []string
	Crop         string  // absolute path of the line's picture, "" if none
	SecondDigits string  // the amount check's reading of the cash
	SecondGold   float64 // and of the gold
	Checked      bool    // the family confirmed the gift in chat; Flags is then empty
}

// Entries returns every line in the notebook, page by page.
func (s *Store) Entries() ([]Entry, error) {
	rows, err := s.db.Query(`SELECT l.id, p.function, p.date, l.number, l.text, l.name, l.place,
		l.amount_digits, l.gold, l.item, l.flags, l.crop, l.second_digits, l.second_gold, l.checked
		FROM lines l JOIN pages p ON p.id = l.page_id ORDER BY p.id, l.idx`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var flags string
		if err := rows.Scan(&e.ID, &e.Function, &e.Date, &e.Number, &e.Text, &e.Name, &e.Place,
			&e.AmountDigits, &e.Gold, &e.Item, &flags, &e.Crop, &e.SecondDigits, &e.SecondGold, &e.Checked); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(flags), &e.Flags)
		if e.Crop != "" {
			e.Crop = filepath.Join(s.dir, e.Crop)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Mode returns what a chat is doing: "pages" while adding notebook pages, else "".
func (s *Store) Mode(chatID int64) (string, error) {
	m, _, err := s.ModeSince(chatID)
	return m, err
}

// ModeSince is Mode plus when it was last set (zero if never recorded).
func (s *Store) ModeSince(chatID int64) (string, time.Time, error) {
	var m, at string
	err := s.db.QueryRow(`SELECT mode, set_at FROM chats WHERE chat_id = ?`, chatID).Scan(&m, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, nil
	}
	t, _ := time.Parse(time.RFC3339, at)
	return m, t, err
}

func (s *Store) SetMode(chatID int64, mode string) error {
	_, err := s.db.Exec(`INSERT INTO chats (chat_id, mode, set_at) VALUES (?, ?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET mode = excluded.mode, set_at = excluded.set_at`,
		chatID, strings.TrimSpace(mode), time.Now().UTC().Format(time.RFC3339))
	return err
}

// maxAttempts is how many times one Telegram update may be started. A second
// try covers a reply cut off mid-way (the process restarted, or the ledger
// failed); more would risk a loop on an update that always fails.
const maxAttempts = 2

// Claim reports whether this Telegram update should be handled now: it is
// new, or an earlier try never finished. Call Finish once it is handled.
// Callers handle one update at a time, so an unfinished try seen here is one
// that died, not one still running. Kept on disk, so it survives restarts.
func (s *Store) Claim(updateID int64) (bool, error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO updates (update_id, done, attempts) VALUES (?, 0, 1)`, updateID)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return true, nil
	}
	res, err = s.db.Exec(`UPDATE updates SET attempts = attempts + 1 WHERE update_id = ? AND done = 0 AND attempts < ?`,
		updateID, maxAttempts)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// Finish marks a claimed update as handled, so a re-delivery is dropped.
func (s *Store) Finish(updateID int64) error {
	_, err := s.db.Exec(`UPDATE updates SET done = 1 WHERE update_id = ?`, updateID)
	return err
}

// RecordCalls stores the model calls made for one update.
func (s *Store) RecordCalls(updateID int64, calls []vision.Call) error {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, c := range calls {
		if _, err := s.db.Exec(`INSERT INTO calls (at, update_id, model, ok, error, elapsed_ms, in_tokens, out_tokens, thinking, cost_usd)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			now, updateID, c.Model, c.OK, c.Err, c.Elapsed.Milliseconds(), c.In, c.Out, c.Thinking, c.CostUSD); err != nil {
			return err
		}
	}
	return nil
}

// ModelUsage is what one model was asked to do, summed.
type ModelUsage struct {
	Model   string
	Calls   int
	Failed  int
	In, Out int
	CostUSD float64
	AvgSecs float64 // mean time of the successful calls
}

// Usage sums the model calls made since the given time, one row per model.
func (s *Store) Usage(since time.Time) ([]ModelUsage, error) {
	rows, err := s.db.Query(`SELECT model, count(*), coalesce(sum(ok = 0), 0), coalesce(sum(in_tokens), 0), coalesce(sum(out_tokens), 0),
		coalesce(sum(cost_usd), 0), coalesce(avg(CASE WHEN ok THEN elapsed_ms END), 0) / 1000.0
		FROM calls WHERE at >= ? GROUP BY model ORDER BY model`, since.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelUsage
	for rows.Next() {
		var u ModelUsage
		if err := rows.Scan(&u.Model, &u.Calls, &u.Failed, &u.In, &u.Out, &u.CostUSD, &u.AvgSecs); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Family returns what the household told us: "father", "mother", and relation
// words, each with a name.
func (s *Store) Family() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT relation, name FROM family`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var r, n string
		if err := rows.Scan(&r, &n); err != nil {
			return nil, err
		}
		out[r] = n
	}
	return out, rows.Err()
}

// SetFamily records who a relation is; an empty name forgets it.
func (s *Store) SetFamily(relation, name string) error {
	if strings.TrimSpace(name) == "" {
		_, err := s.db.Exec(`DELETE FROM family WHERE relation = ?`, relation)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO family (relation, name) VALUES (?, ?)
		ON CONFLICT(relation) DO UPDATE SET name = excluded.name`, relation, strings.TrimSpace(name))
	return err
}

// Ask is one "Is this them?" question sent with Yes/No buttons.
type Ask struct {
	ID       int64
	LineID   int64
	Person   string
	Relation string
}

func (s *Store) AddAsk(a Ask) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO asks (line_id, person, relation, asked_at) VALUES (?, ?, ?, ?)`,
		a.LineID, a.Person, a.Relation, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetAsk(id int64) (Ask, bool, error) {
	a := Ask{ID: id}
	err := s.db.QueryRow(`SELECT line_id, person, relation FROM asks WHERE id = ?`, id).Scan(&a.LineID, &a.Person, &a.Relation)
	if errors.Is(err, sql.ErrNoRows) {
		return Ask{}, false, nil
	}
	return a, err == nil, err
}

// SetAnswer remembers whether a line is this person; a later answer replaces it.
func (s *Store) SetAnswer(personKey string, lineID int64, yes bool) error {
	_, err := s.db.Exec(`INSERT INTO answers (person_key, line_id, yes, answered_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(person_key, line_id) DO UPDATE SET yes = excluded.yes, answered_at = excluded.answered_at`,
		personKey, lineID, yes, time.Now().UTC().Format(time.RFC3339))
	return err
}

// Answers returns every remembered answer: person key -> line ID -> yes.
func (s *Store) Answers() (map[string]map[int64]bool, error) {
	rows, err := s.db.Query(`SELECT person_key, line_id, yes FROM answers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[int64]bool{}
	for rows.Next() {
		var k string
		var id int64
		var yes bool
		if err := rows.Scan(&k, &id, &yes); err != nil {
			return nil, err
		}
		if out[k] == nil {
			out[k] = map[int64]bool{}
		}
		out[k][id] = yes
	}
	return out, rows.Err()
}

// Entry returns one line by ID.
func (s *Store) Entry(id int64) (Entry, bool, error) {
	es, err := s.Entries()
	if err != nil {
		return Entry{}, false, err
	}
	for _, e := range es {
		if e.ID == id {
			return e, true, nil
		}
	}
	return Entry{}, false, nil
}

// ConfirmGift records the gift as the family read it from the handwriting:
// it replaces the model's reading and clears the line's flags.
func (s *Store) ConfirmGift(lineID int64, digits string, gold float64) error {
	res, err := s.db.Exec(`UPDATE lines SET amount_digits = ?, gold = ?, flags = '[]', checked = 1 WHERE id = ?`,
		digits, gold, lineID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("line %d not found", lineID)
	}
	return nil
}

// SamePage finds a stored page with the same gifts as p: another photo of a
// page already read. Two photos of one page differ byte for byte, so the photo
// hash can't tell. Pages are the same when at least 3 lines, and 60% of p's
// lines with money or gold, have the same line number and the same amount.
func (s *Store) SamePage(p notebook.Page) (PageInfo, bool, error) {
	key := func(number, digits string, gold float64) string {
		if digits == "" && gold == 0 {
			return "" // an item (saree, plate) alone says too little
		}
		return fmt.Sprintf("%s|%s|%g", strings.TrimSpace(number), digits, gold)
	}
	want := map[string]bool{}
	for _, l := range p.Lines {
		if k := key(l.Number, l.AmountDigits, l.GoldSovereigns); k != "" {
			want[k] = true
		}
	}
	if len(want) < 3 {
		return PageInfo{}, false, nil
	}
	rows, err := s.db.Query(`SELECT page_id, number, amount_digits, gold FROM lines`)
	if err != nil {
		return PageInfo{}, false, err
	}
	same := map[int64]int{}
	for rows.Next() {
		var id int64
		var number, digits string
		var gold float64
		if err := rows.Scan(&id, &number, &digits, &gold); err != nil {
			rows.Close()
			return PageInfo{}, false, err
		}
		if want[key(number, digits, gold)] {
			same[id]++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return PageInfo{}, false, err
	}
	var best int64
	for id, n := range same {
		if n >= 3 && n*10 >= len(want)*6 && (best == 0 || n > same[best]) {
			best = id
		}
	}
	if best == 0 {
		return PageInfo{}, false, nil
	}
	var hash string
	if err := s.db.QueryRow(`SELECT photo_hash FROM pages WHERE id = ?`, best).Scan(&hash); err != nil {
		return PageInfo{}, false, err
	}
	return s.PageByHash(hash)
}
