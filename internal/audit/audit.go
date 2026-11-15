// Package audit is the append-only, hash-chained administrative audit log
// (A-15, R-11). A failed write must fail the action that caused it.
package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// Entry is one audit record.
type Entry struct {
	Seq            int64          `json:"seq"`
	At             time.Time      `json:"at"`
	ActorUserID    string         `json:"actorUserId"`
	ActorName      string         `json:"actorName"`
	TailscaleLogin string         `json:"tailscaleLogin"`
	Action         string         `json:"action"`
	Target         string         `json:"target"`
	Params         map[string]any `json:"params,omitempty"`
	Result         string         `json:"result"`
	RequestID      string         `json:"requestId"`
}

// Log writes and reads audit entries.
type Log struct {
	db  *sql.DB
	mu  sync.Mutex
	Now func() time.Time
}

// New returns a log on db.
func New(db *sql.DB) *Log { return &Log{db: db, Now: time.Now} }

func chain(prev string, e Entry, params string) string {
	h := sha256.New()
	for _, f := range []string{prev, e.At.UTC().Format(time.RFC3339Nano), e.ActorUserID, e.ActorName,
		e.TailscaleLogin, e.Action, e.Target, params, e.Result, e.RequestID} {
		h.Write([]byte(f))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Write appends e. Params must already be free of secrets.
func (l *Log) Write(ctx context.Context, e Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	e.At = l.Now().UTC().Truncate(time.Millisecond)
	params := "{}"
	if len(e.Params) > 0 {
		b, err := json.Marshal(e.Params)
		if err != nil {
			return err
		}
		params = string(b)
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var prev string
	err = tx.QueryRowContext(ctx, `SELECT hash FROM audit ORDER BY seq DESC LIMIT 1`).Scan(&prev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit (at, prev_hash, hash, actor_user_id, actor_name, tailscale_login, action, target, params, result, request_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`, e.At.UnixMilli(), prev, chain(prev, e, params), e.ActorUserID, e.ActorName,
		e.TailscaleLogin, e.Action, e.Target, params, e.Result, e.RequestID); err != nil {
		return err
	}
	return tx.Commit()
}

type row struct {
	Entry
	prev, hash, params string
}

func (l *Log) query(ctx context.Context, q string, args ...any) ([]row, error) {
	rows, err := l.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []row
	for rows.Next() {
		var r row
		var at int64
		if err := rows.Scan(&r.Seq, &at, &r.prev, &r.hash, &r.ActorUserID, &r.ActorName, &r.TailscaleLogin,
			&r.Action, &r.Target, &r.params, &r.Result, &r.RequestID); err != nil {
			return nil, err
		}
		r.At = time.UnixMilli(at).UTC()
		if r.params != "{}" {
			_ = json.Unmarshal([]byte(r.params), &r.Params)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const cols = `seq, at, prev_hash, hash, actor_user_id, actor_name, tailscale_login, action, target, params, result, request_id`

// List returns up to limit entries older than before (0 = newest), newest first.
func (l *Log) List(ctx context.Context, before int64, limit int, actor, action string) ([]Entry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT ` + cols + ` FROM audit WHERE 1=1`
	var args []any
	if before > 0 {
		q += ` AND seq < ?`
		args = append(args, before)
	}
	if actor != "" {
		q += ` AND (actor_user_id = ? OR actor_name = ?)`
		args = append(args, actor, actor)
	}
	if action != "" {
		q += ` AND action = ?`
		args = append(args, action)
	}
	rows, err := l.query(ctx, q+` ORDER BY seq DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, len(rows))
	for i, r := range rows {
		out[i] = r.Entry
	}
	return out, nil
}

// Verify recomputes the chain. It returns the first broken seq, or 0.
func (l *Log) Verify(ctx context.Context) (int64, error) {
	rows, err := l.query(ctx, `SELECT `+cols+` FROM audit ORDER BY seq ASC`)
	if err != nil {
		return 0, err
	}
	prev := ""
	for i, r := range rows {
		if i > 0 && r.prev != prev {
			return r.Seq, nil
		}
		if chain(r.prev, r.Entry, r.params) != r.hash {
			return r.Seq, nil
		}
		prev = r.hash
	}
	return 0, nil
}

// Prune removes entries older than the retention period (365 days).
func (l *Log) Prune(ctx context.Context) error {
	_, err := l.db.ExecContext(ctx, `DELETE FROM audit WHERE at < ?`, l.Now().Add(-365*24*time.Hour).UnixMilli())
	return err
}
