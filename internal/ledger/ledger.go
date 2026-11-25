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
	crop          TEXT NOT NULL     -- path of the line's picture, relative to the data dir; "" if none
);
CREATE TABLE IF NOT EXISTS chats (
	chat_id INTEGER PRIMARY KEY,
	mode    TEXT NOT NULL           -- "pages" while the family is adding notebook pages
);
CREATE TABLE IF NOT EXISTS updates (
	update_id INTEGER PRIMARY KEY   -- Telegram updates already handled (it re-delivers on slow replies)
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
	return &Store{db: db, dir: dir}, nil
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
		if _, err := tx.Exec(`INSERT INTO lines (page_id, idx, number, text, name, place, amount_digits, gold, item, flags, crop)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			pageID, i, l.Number, l.Text, l.Name, l.Place, l.AmountDigits, l.GoldSovereigns, l.Item, string(fj), crop); err != nil {
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
	Crop         string // absolute path of the line's picture, "" if none
}

// Entries returns every line in the notebook, page by page.
func (s *Store) Entries() ([]Entry, error) {
	rows, err := s.db.Query(`SELECT l.id, p.function, p.date, l.number, l.text, l.name, l.place,
		l.amount_digits, l.gold, l.item, l.flags, l.crop
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
			&e.AmountDigits, &e.Gold, &e.Item, &flags, &e.Crop); err != nil {
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
	var m string
	err := s.db.QueryRow(`SELECT mode FROM chats WHERE chat_id = ?`, chatID).Scan(&m)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return m, err
}

func (s *Store) SetMode(chatID int64, mode string) error {
	_, err := s.db.Exec(`INSERT INTO chats (chat_id, mode) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET mode = excluded.mode`, chatID, strings.TrimSpace(mode))
	return err
}

// FirstDelivery records a Telegram update and reports whether it is new. It
// survives restarts, unlike an in-memory set.
func (s *Store) FirstDelivery(updateID int64) (bool, error) {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO updates (update_id) VALUES (?)`, updateID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}
