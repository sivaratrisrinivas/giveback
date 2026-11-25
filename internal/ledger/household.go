package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Splitting a household. Sprites have no fork, so when a son or daughter sets
// up their own home, the notebook is copied: CopyFor writes a complete,
// consistent copy of this ledger into a new directory, which then goes to the
// new household's own Sprite. From then on the two notebooks grow apart.

const householdSchema = `
CREATE TABLE IF NOT EXISTS household (
	id          INTEGER PRIMARY KEY CHECK (id = 1), -- one row: whose notebook this is
	name        TEXT NOT NULL,
	copied_from TEXT NOT NULL DEFAULT '', -- the household this notebook was copied from; '' if it began here
	copied_at   TEXT NOT NULL DEFAULT '', -- UTC RFC 3339
	pages       INTEGER NOT NULL DEFAULT 0, -- what the copy carried
	lines       INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS copies (
	name  TEXT NOT NULL, -- a household that got a copy of this notebook
	at    TEXT NOT NULL, -- UTC RFC 3339
	pages INTEGER NOT NULL,
	lines INTEGER NOT NULL
);`

// Household says whose notebook this is and where it came from.
type Household struct {
	Name       string // "" until a copy is first made
	CopiedFrom string // "" if the notebook began here
	CopiedAt   time.Time
	Pages      int // how much the copy carried
	Lines      int
	Copies     []Copy // households that got a copy of this notebook, oldest first
}

// Copy is one copy made of this notebook for a new household.
type Copy struct {
	Name         string
	At           time.Time
	Pages, Lines int
}

// Household returns whose notebook this is.
func (s *Store) Household() (Household, error) {
	var h Household
	var at string
	err := s.db.QueryRow(`SELECT name, copied_from, copied_at, pages, lines FROM household WHERE id = 1`).
		Scan(&h.Name, &h.CopiedFrom, &at, &h.Pages, &h.Lines)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return h, err
	}
	h.CopiedAt, _ = time.Parse(time.RFC3339, at)
	rows, err := s.db.Query(`SELECT name, at, pages, lines FROM copies ORDER BY rowid`)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Copy
		if err := rows.Scan(&c.Name, &at, &c.Pages, &c.Lines); err != nil {
			return h, err
		}
		c.At, _ = time.Parse(time.RFC3339, at)
		h.Copies = append(h.Copies, c)
	}
	return h, rows.Err()
}

// CopyFor writes a copy of this notebook for the household newName into dir,
// which must not exist yet. from names this household if it has no name yet.
//
// The copy carries the pages, every line and its picture, the family tree, the
// family's Yes/No answers and confirmed amounts, and the model-answer cache
// (so the same photo is never read twice in either home). It leaves out what
// belonged to this household's chat bot: Telegram update IDs (each bot counts
// its own, so an old ID could make the new bot drop a new message), chat
// modes, open questions, and the model cost log.
//
// It is safe while the bot is running: the database is copied with SQLite's
// VACUUM INTO, which reads one consistent snapshot.
func (s *Store) CopyFor(dir, from, newName string) (Copy, error) {
	if newName == "" {
		return Copy{}, errors.New("the new household needs a name")
	}
	h, err := s.Household()
	if err != nil {
		return Copy{}, err
	}
	if h.Name == "" {
		if from == "" {
			return Copy{}, errors.New("this household has no name yet: give one")
		}
		h.Name = from
		if _, err := s.db.Exec(`INSERT INTO household (id, name) VALUES (1, ?)`, from); err != nil {
			return Copy{}, err
		}
	}
	if h.Name == newName {
		return Copy{}, fmt.Errorf("the new household needs a name other than %q", newName)
	}
	if _, err := os.Stat(dir); err == nil {
		return Copy{}, fmt.Errorf("%s already exists", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Copy{}, err
	}
	if _, err := s.db.Exec(`VACUUM INTO ?`, filepath.Join(dir, "ledger.db")); err != nil {
		return Copy{}, fmt.Errorf("snapshot ledger: %w", err)
	}
	// Pictures after the snapshot, so every line in it has its picture.
	for _, sub := range []string{"crops", "cache"} {
		if err := copyTree(filepath.Join(s.dir, sub), filepath.Join(dir, sub)); err != nil {
			return Copy{}, fmt.Errorf("copy %s: %w", sub, err)
		}
	}

	c := Copy{Name: newName, At: time.Now().UTC().Truncate(time.Second)}
	at := c.At.Format(time.RFC3339)
	n, err := Open(dir)
	if err != nil {
		return Copy{}, err
	}
	defer n.Close()
	if err := n.db.QueryRow(`SELECT (SELECT count(*) FROM pages), (SELECT count(*) FROM lines)`).Scan(&c.Pages, &c.Lines); err != nil {
		return Copy{}, err
	}
	tx, err := n.db.Begin()
	if err != nil {
		return Copy{}, err
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM updates`, `DELETE FROM chats`, `DELETE FROM asks`, `DELETE FROM calls`, `DELETE FROM copies`, `DELETE FROM household`} {
		if _, err := tx.Exec(q); err != nil {
			return Copy{}, err
		}
	}
	if _, err := tx.Exec(`INSERT INTO household (id, name, copied_from, copied_at, pages, lines) VALUES (1, ?, ?, ?, ?, ?)`,
		newName, h.Name, at, c.Pages, c.Lines); err != nil {
		return Copy{}, err
	}
	if err := tx.Commit(); err != nil {
		return Copy{}, err
	}
	// Copying again for the same household (it was set up anew) replaces the old record.
	if _, err := s.db.Exec(`DELETE FROM copies WHERE name = ?`, newName); err != nil {
		return Copy{}, err
	}
	if _, err := s.db.Exec(`INSERT INTO copies (name, at, pages, lines) VALUES (?, ?, ?, ?)`, newName, at, c.Pages, c.Lines); err != nil {
		return Copy{}, err
	}
	return c, nil
}

// copyTree copies the regular files under src to dst. A missing src is fine.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == src {
			return nil
		}
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}
