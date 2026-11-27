package ledger

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// The public demo bot shares one made-up notebook with every visitor. What a
// visitor tells it (Yes/No answers, who a relation word means, a confirmed
// amount) is kept for that chat only, so one visitor never changes what the
// next one sees. The shared tables stay as they were seeded.
const demoSchema = `
CREATE TABLE IF NOT EXISTS demo_answers (
	chat_id     INTEGER NOT NULL,
	person_key  TEXT NOT NULL,
	line_id     INTEGER NOT NULL,
	yes         INTEGER NOT NULL,
	answered_at TEXT NOT NULL,
	PRIMARY KEY (chat_id, person_key, line_id)
);
CREATE TABLE IF NOT EXISTS demo_family (
	chat_id  INTEGER NOT NULL,
	relation TEXT NOT NULL,
	name     TEXT NOT NULL,             -- "" means this chat removed it
	PRIMARY KEY (chat_id, relation)
);
CREATE TABLE IF NOT EXISTS demo_gifts (
	chat_id INTEGER NOT NULL,
	line_id INTEGER NOT NULL,
	digits  TEXT NOT NULL,
	gold    REAL NOT NULL,
	PRIMARY KEY (chat_id, line_id)
);
CREATE TABLE IF NOT EXISTS demo_cards (
	day     TEXT NOT NULL,              -- UTC date: the daily cap on card reads
	chat_id INTEGER NOT NULL,
	n       INTEGER NOT NULL,
	PRIMARY KEY (day, chat_id)
);`

// ForChat is a view of the ledger for one demo visitor: it reads the shared
// notebook with this chat's own answers, family links and confirmed amounts
// on top, and writes those only for this chat.
func (s *Store) ForChat(chatID int64) *Store {
	return &Store{db: s.db, dir: s.dir, chat: chatID, perChat: true}
}

func (s *Store) chatFamily(out map[string]string) error {
	rows, err := s.db.Query(`SELECT relation, name FROM demo_family WHERE chat_id = ?`, s.chat)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r, n string
		if err := rows.Scan(&r, &n); err != nil {
			return err
		}
		if n == "" {
			delete(out, r)
		} else {
			out[r] = n
		}
	}
	return rows.Err()
}

func (s *Store) setChatFamily(relation, name string) error {
	_, err := s.db.Exec(`INSERT INTO demo_family (chat_id, relation, name) VALUES (?, ?, ?)
		ON CONFLICT(chat_id, relation) DO UPDATE SET name = excluded.name`, s.chat, relation, strings.TrimSpace(name))
	return err
}

func (s *Store) chatAnswers(out map[string]map[int64]bool) error {
	rows, err := s.db.Query(`SELECT person_key, line_id, yes FROM demo_answers WHERE chat_id = ?`, s.chat)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var id int64
		var yes bool
		if err := rows.Scan(&k, &id, &yes); err != nil {
			return err
		}
		if out[k] == nil {
			out[k] = map[int64]bool{}
		}
		out[k][id] = yes
	}
	return rows.Err()
}

func (s *Store) setChatAnswer(personKey string, lineID int64, yes bool) error {
	_, err := s.db.Exec(`INSERT INTO demo_answers (chat_id, person_key, line_id, yes, answered_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(chat_id, person_key, line_id) DO UPDATE SET yes = excluded.yes, answered_at = excluded.answered_at`,
		s.chat, personKey, lineID, yes, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *Store) confirmChatGift(lineID int64, digits string, gold float64) error {
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM lines WHERE id = ?`, lineID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return errors.New("line not found")
	}
	_, err := s.db.Exec(`INSERT INTO demo_gifts (chat_id, line_id, digits, gold) VALUES (?, ?, ?, ?)
		ON CONFLICT(chat_id, line_id) DO UPDATE SET digits = excluded.digits, gold = excluded.gold`, s.chat, lineID, digits, gold)
	return err
}

// chatGifts puts this chat's confirmed amounts over the shared lines.
func (s *Store) chatGifts(es []Entry) error {
	rows, err := s.db.Query(`SELECT line_id, digits, gold FROM demo_gifts WHERE chat_id = ?`, s.chat)
	if err != nil {
		return err
	}
	defer rows.Close()
	type gift struct {
		digits string
		gold   float64
	}
	mine := map[int64]gift{}
	for rows.Next() {
		var id int64
		var g gift
		if err := rows.Scan(&id, &g.digits, &g.gold); err != nil {
			return err
		}
		mine[id] = g
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i, e := range es {
		if g, ok := mine[e.ID]; ok {
			es[i].AmountDigits, es[i].Gold, es[i].Flags, es[i].Checked = g.digits, g.gold, nil, true
		}
	}
	return nil
}

// CardReads says how many cards the demo read today: in all, and for this chat.
func (s *Store) CardReads(day string) (total, mine int, err error) {
	err = s.db.QueryRow(`SELECT coalesce(sum(n), 0), coalesce(sum(CASE WHEN chat_id = ? THEN n END), 0)
		FROM demo_cards WHERE day = ?`, s.chat, day).Scan(&total, &mine)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return total, mine, err
}

// AddCardRead counts one card read by a model (cached answers are free).
func (s *Store) AddCardRead(day string) error {
	_, err := s.db.Exec(`INSERT INTO demo_cards (day, chat_id, n) VALUES (?, ?, 1)
		ON CONFLICT(day, chat_id) DO UPDATE SET n = n + 1`, day, s.chat)
	return err
}
