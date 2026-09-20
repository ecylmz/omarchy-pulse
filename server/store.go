package main

import (
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite"
)

// SPEC §15.2: aggregate counts only. No key, no session, no address, and no
// per-user row ever reaches this table.
const schema = `
CREATE TABLE IF NOT EXISTS presence_history (
    scope  TEXT    NOT NULL,
    code   TEXT    NOT NULL,
    ts     INTEGER NOT NULL,
    online INTEGER NOT NULL,
    active INTEGER NOT NULL DEFAULT 0,
    focus  INTEGER NOT NULL DEFAULT 0,
    away   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (scope, code, ts)
) WITHOUT ROWID;
`

type store struct{ db *sql.DB }

func openStore(path string) (*store, error) {
	dsn := "file:" + url.PathEscape(path) +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// ponytail: one connection, so writer/reader locking cannot happen at
	// all. Split into read and write pools only if the history endpoint ever
	// shows contention — at a five-minute write cadence it will not.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &store{db: db}, nil
}

func (s *store) Close() error { return s.db.Close() }

func (s *store) writeSnapshot(ts int64, rows []scopeCount) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO presence_history
		(scope, code, ts, online, active) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range rows {
		if _, err := stmt.Exec(r.Scope, r.Code, ts, r.Online, r.Online); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// points returns the series for one scope. Gaps are left as gaps: a missing
// timestamp means nobody was there, which the client renders as zero.
func (s *store) points(scope, code string, since int64) ([][2]int64, error) {
	rows, err := s.db.Query(
		`SELECT ts, online FROM presence_history
		 WHERE scope = ? AND code = ? AND ts >= ? ORDER BY ts`, scope, code, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := [][2]int64{}
	for rows.Next() {
		var ts, online int64
		if err := rows.Scan(&ts, &online); err != nil {
			return nil, err
		}
		out = append(out, [2]int64{ts, online})
	}
	return out, rows.Err()
}

// stats collects the three headline numbers in one round trip.
func (s *store) stats(scope, code string, dayStart, weekStart int64) (dayPeak, dayLow, weekPeak int, err error) {
	var p, l, w sql.NullInt64
	err = s.db.QueryRow(
		`SELECT MAX(CASE WHEN ts >= ?1 THEN online END),
		        MIN(CASE WHEN ts >= ?1 THEN online END),
		        MAX(online)
		 FROM presence_history WHERE scope = ?2 AND code = ?3 AND ts >= ?4`,
		dayStart, scope, code, weekStart).Scan(&p, &l, &w)
	if err != nil {
		return 0, 0, 0, err
	}
	return int(p.Int64), int(l.Int64), int(w.Int64), nil
}

// weekPeaks is the baseline the anomaly check compares against (SPEC §6).
func (s *store) weekPeaks(since int64) (map[string]int, error) {
	rows, err := s.db.Query(
		`SELECT scope, code, MAX(online) FROM presence_history
		 WHERE ts >= ? GROUP BY scope, code`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	peaks := map[string]int{}
	for rows.Next() {
		var scope, code string
		var peak int
		if err := rows.Scan(&scope, &code, &peak); err != nil {
			return nil, err
		}
		peaks[scope+"/"+code] = peak
	}
	return peaks, rows.Err()
}
