package daemon

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	acp "github.com/gfx-labs/go-acp"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	cwd TEXT NOT NULL,
	title TEXT NOT NULL DEFAULT '',
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
	session TEXT NOT NULL,
	seq INTEGER NOT NULL,
	body BLOB NOT NULL,
	PRIMARY KEY (session, seq)
);
CREATE VIRTUAL TABLE IF NOT EXISTS messages USING fts5(session UNINDEXED, kind UNINDEXED, text);
`

// Store is the daemon's SQLite database: the sessions it has seen, the
// session/update stream of each one for replay, and a full text index of
// the messages in them.
type Store struct {
	db *sql.DB
}

// OpenStore opens (creating if needed) the database at path.
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// One connection: writes come from one goroutine at a time anyway, and
	// a second connection would only add lock contention.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// SessionRow is one stored session.
type SessionRow struct {
	ID        string `json:"id"`
	Cwd       string `json:"cwd"`
	Title     string `json:"title"`
	UpdatedAt int64  `json:"updatedAt"`
}

// Touch records a session's working directory and marks it updated now.
func (s *Store) Touch(ctx context.Context, id acp.SessionId, cwd string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (id, cwd, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET cwd = excluded.cwd, updated_at = excluded.updated_at`,
		string(id), cwd, time.Now().UnixMilli())
	return err
}

// Remember records a session reported by the agent's session/list
// without changing a newer local record.
func (s *Store) Remember(ctx context.Context, info acp.SessionInfo) error {
	at := time.Now().UnixMilli()
	if info.UpdatedAt != nil {
		if t, err := time.Parse(time.RFC3339, *info.UpdatedAt); err == nil {
			at = t.UnixMilli()
		}
	}
	title := ""
	if info.Title != nil {
		title = *info.Title
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (id, cwd, title, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET cwd = excluded.cwd,
			title = CASE WHEN excluded.title != '' THEN excluded.title ELSE sessions.title END,
			updated_at = max(sessions.updated_at, excluded.updated_at)`,
		string(info.SessionId), info.Cwd, title, at)
	return err
}

// Session returns one stored session.
func (s *Store) Session(ctx context.Context, id acp.SessionId) (SessionRow, error) {
	var r SessionRow
	err := s.db.QueryRowContext(ctx, `SELECT id, cwd, title, updated_at FROM sessions WHERE id = ?`, string(id)).
		Scan(&r.ID, &r.Cwd, &r.Title, &r.UpdatedAt)
	return r, err
}

// Sessions lists stored sessions, most recently updated first.
func (s *Store) Sessions(ctx context.Context) ([]SessionRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, cwd, title, updated_at FROM sessions ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SessionRow
	for rows.Next() {
		var r SessionRow
		if err := rows.Scan(&r.ID, &r.Cwd, &r.Title, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Reset drops a session's stored updates and index, before the agent
// replays the whole session again.
func (s *Store) Reset(ctx context.Context, id acp.SessionId) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE session = ?`, string(id)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE session = ?`, string(id)); err != nil {
		return err
	}
	return tx.Commit()
}

// Append stores one update at the end of a session's stream.
func (s *Store) Append(ctx context.Context, id acp.SessionId, u acp.SessionUpdate) error {
	body, err := json.Marshal(u)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO events (session, seq, body)
		VALUES (?, (SELECT coalesce(max(seq), 0) + 1 FROM events WHERE session = ?), ?)`,
		string(id), string(id), body)
	if err != nil {
		return err
	}
	if info := u.SessionInfoUpdate; info != nil && info.Title != nil {
		_, err = s.db.ExecContext(ctx, `UPDATE sessions SET title = ?, updated_at = ? WHERE id = ?`,
			*info.Title, time.Now().UnixMilli(), string(id))
	}
	return err
}

// Index adds one finished message to the full text index.
func (s *Store) Index(ctx context.Context, id acp.SessionId, kind, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO messages (session, kind, text) VALUES (?, ?, ?)`, string(id), kind, text)
	return err
}

// Replay calls f with each stored update of a session in order.
func (s *Store) Replay(ctx context.Context, id acp.SessionId, f func(acp.SessionUpdate)) error {
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM events WHERE session = ? ORDER BY seq`, string(id))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return err
		}
		var u acp.SessionUpdate
		if err := json.Unmarshal(body, &u); err != nil {
			return fmt.Errorf("stored update of %s: %w", id, err)
		}
		f(u)
	}
	return rows.Err()
}

// Hit is one search result.
type Hit struct {
	Session string `json:"session"`
	Title   string `json:"title"`
	Cwd     string `json:"cwd"`
	Kind    string `json:"kind"`
	Snippet string `json:"snippet"`
}

// Search finds messages containing every word of query, best match first.
// A non-empty session limits the search to that session.
func (s *Store) Search(ctx context.Context, query string, session acp.SessionId, limit int) ([]Hit, error) {
	match := ftsQuery(query)
	if match == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT m.session, coalesce(s.title, ''), coalesce(s.cwd, ''), m.kind,
			snippet(messages, 2, '[', ']', '…', 16)
		FROM messages m LEFT JOIN sessions s ON s.id = m.session
		WHERE messages MATCH ? AND (? = '' OR m.session = ?) ORDER BY rank LIMIT ?`, match, string(session), string(session), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.Session, &h.Title, &h.Cwd, &h.Kind, &h.Snippet); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ftsQuery quotes each word so FTS5 operators in user input are literal.
func ftsQuery(q string) string {
	var terms []string
	for _, w := range strings.Fields(q) {
		terms = append(terms, `"`+strings.ReplaceAll(w, `"`, `""`)+`"`)
	}
	return strings.Join(terms, " ")
}
