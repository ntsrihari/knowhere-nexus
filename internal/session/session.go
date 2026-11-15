// Package session implements Nexus sessions (A-10, A-16, R-01, R-02, R-14):
// opaque 256-bit tokens stored as SHA-256 hashes, 15-minute access tokens,
// single-use rotating refresh tokens with a 20 s idempotent grace window,
// reuse detection, a 7-day absolute cap and immediate revocation.
package session

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// Durations (R-01, R-14, Q-02, P-11).
const (
	AccessTTL    = 15 * time.Minute
	AbsoluteTTL  = 7 * 24 * time.Hour
	RefreshGrace = 20 * time.Second
	AdminWindow  = 12 * time.Hour
	ReauthWindow = 10 * time.Minute
)

// Errors.
var (
	ErrInvalid = errors.New("invalid or expired credential")
	ErrReuse   = errors.New("refresh token reuse detected; session revoked")
)

// Kinds and roles.
const (
	KindBrowser = "browser"
	KindMilano  = "milano"
	RoleAdmin   = "admin"
	RoleUser    = "user"
)

// New describes a session to create.
type New struct {
	UserID, Username string
	IsAdmin          bool
	Kind             string
	AuthMethod       string // password | quickconnect | exchange
	DeviceLabel      string
	TailscaleLogin   string
}

// Session is an active session family.
type Session struct {
	FamilyID          string    `json:"id"`
	UserID            string    `json:"userId"`
	Username          string    `json:"username"`
	Role              string    `json:"role"`
	RoleVerifiedAt    time.Time `json:"roleVerifiedAt"`
	ReauthAt          time.Time `json:"-"`
	Kind              string    `json:"kind"`
	AuthMethod        string    `json:"authMethod"`
	DeviceLabel       string    `json:"deviceLabel"`
	TailscaleLogin    string    `json:"tailscaleLogin"`
	CreatedAt         time.Time `json:"createdAt"`
	LastSeenAt        time.Time `json:"lastSeenAt"`
	AbsoluteExpiresAt time.Time `json:"absoluteExpiresAt"`
}

// IsAdmin reports whether the admin role is in force at now: the person is a
// Jellyfin admin and was verified within AdminWindow (Q-02).
func (s Session) IsAdmin(now time.Time) bool {
	return s.Role == RoleAdmin && now.Sub(s.RoleVerifiedAt) < AdminWindow
}

// EffectiveRole returns "admin" or "user" at now.
func (s Session) EffectiveRole(now time.Time) string {
	if s.IsAdmin(now) {
		return RoleAdmin
	}
	return RoleUser
}

// RecentlyReauthenticated reports a password re-auth within ReauthWindow.
func (s Session) RecentlyReauthenticated(now time.Time) bool {
	return !s.ReauthAt.IsZero() && now.Sub(s.ReauthAt) < ReauthWindow
}

// Tokens are the credentials handed to a client.
type Tokens struct {
	Access          string    `json:"accessToken"`
	Refresh         string    `json:"refreshToken"`
	AccessExpires   time.Time `json:"accessExpiresAt"`
	AbsoluteExpires time.Time `json:"absoluteExpiresAt"`
}

// Store persists sessions in SQLite.
type Store struct {
	db   *sql.DB
	aead cipher.AEAD
	Now  func() time.Time
}

// NewStore returns a store. key is the 32-byte session encryption key.
func NewStore(db *sql.DB, key []byte) (*Store, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}
	return &Store{db: db, aead: aead, Now: time.Now}, nil
}

func newToken() string {
	var b [32]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func hash(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

// Create starts a new session family (a fresh id at every login).
func (s *Store) Create(ctx context.Context, n New) (Tokens, Session, error) {
	now := s.Now().UTC()
	role := RoleUser
	if n.IsAdmin {
		role = RoleAdmin
	}
	sess := Session{
		FamilyID: newToken()[:22], UserID: n.UserID, Username: n.Username, Role: role,
		RoleVerifiedAt: now, Kind: n.Kind, AuthMethod: n.AuthMethod, DeviceLabel: n.DeviceLabel,
		TailscaleLogin: n.TailscaleLogin, CreatedAt: now, LastSeenAt: now, AbsoluteExpiresAt: now.Add(AbsoluteTTL),
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Tokens{}, Session{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO session_families
		(id, user_id, username, role, role_verified_at, kind, auth_method, device_label, tailscale_login, created_at, last_seen_at, absolute_expires_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		sess.FamilyID, sess.UserID, sess.Username, sess.Role, ms(now), sess.Kind, sess.AuthMethod,
		sess.DeviceLabel, sess.TailscaleLogin, ms(now), ms(now), ms(sess.AbsoluteExpiresAt)); err != nil {
		return Tokens{}, Session{}, err
	}
	toks, err := s.mint(ctx, tx, sess.FamilyID, now, sess.AbsoluteExpiresAt)
	if err != nil {
		return Tokens{}, Session{}, err
	}
	return toks, sess, tx.Commit()
}

func (s *Store) mint(ctx context.Context, tx *sql.Tx, family string, now, absolute time.Time) (Tokens, error) {
	t := Tokens{Access: newToken(), Refresh: newToken(), AccessExpires: now.Add(AccessTTL), AbsoluteExpires: absolute}
	if t.AccessExpires.After(absolute) {
		t.AccessExpires = absolute
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO access_tokens (hash, family_id, expires_at) VALUES (?,?,?)`,
		hash(t.Access), family, ms(t.AccessExpires)); err != nil {
		return Tokens{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO refresh_tokens (hash, family_id, expires_at) VALUES (?,?,?)`,
		hash(t.Refresh), family, ms(absolute)); err != nil {
		return Tokens{}, err
	}
	return t, nil
}

const familyCols = `f.id, f.user_id, f.username, f.role, f.role_verified_at, f.reauth_at, f.kind, f.auth_method,
	f.device_label, f.tailscale_login, f.created_at, f.last_seen_at, f.absolute_expires_at, f.revoked_at`

type rowScanner interface{ Scan(...any) error }

func scanFamily(r rowScanner) (Session, int64, error) {
	var s Session
	var verified, reauth, created, seen, abs, revoked int64
	err := r.Scan(&s.FamilyID, &s.UserID, &s.Username, &s.Role, &verified, &reauth, &s.Kind, &s.AuthMethod,
		&s.DeviceLabel, &s.TailscaleLogin, &created, &seen, &abs, &revoked)
	s.RoleVerifiedAt, s.ReauthAt, s.CreatedAt = fromMS(verified), fromMS(reauth), fromMS(created)
	s.LastSeenAt, s.AbsoluteExpiresAt = fromMS(seen), fromMS(abs)
	return s, revoked, err
}

// Authenticate resolves an access token.
func (s *Store) Authenticate(ctx context.Context, access string) (Session, error) {
	if access == "" {
		return Session{}, ErrInvalid
	}
	now := s.Now()
	row := s.db.QueryRowContext(ctx, `SELECT `+familyCols+`, a.expires_at FROM access_tokens a
		JOIN session_families f ON f.id = a.family_id WHERE a.hash = ?`, hash(access))
	var sess Session
	var revoked, exp int64
	var verified, reauth, created, seen, abs int64
	err := row.Scan(&sess.FamilyID, &sess.UserID, &sess.Username, &sess.Role, &verified, &reauth, &sess.Kind,
		&sess.AuthMethod, &sess.DeviceLabel, &sess.TailscaleLogin, &created, &seen, &abs, &revoked, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrInvalid
	}
	if err != nil {
		return Session{}, err
	}
	sess.RoleVerifiedAt, sess.ReauthAt, sess.CreatedAt = fromMS(verified), fromMS(reauth), fromMS(created)
	sess.LastSeenAt, sess.AbsoluteExpiresAt = fromMS(seen), fromMS(abs)
	if revoked != 0 || ms(now) >= exp || ms(now) >= abs {
		return Session{}, ErrInvalid
	}
	if now.Sub(sess.LastSeenAt) > time.Minute {
		_, _ = s.db.ExecContext(ctx, `UPDATE session_families SET last_seen_at = ? WHERE id = ?`, ms(now), sess.FamilyID)
		sess.LastSeenAt = now.UTC()
	}
	return sess, nil
}

type successor struct {
	Access        string    `json:"a"`
	Refresh       string    `json:"r"`
	AccessExpires time.Time `json:"e"`
}

// Refresh rotates a refresh token atomically (R-14). Presenting the same
// token again within RefreshGrace returns the same successor pair; later
// reuse revokes the whole family.
func (s *Store) Refresh(ctx context.Context, refresh string) (Tokens, Session, error) {
	if refresh == "" {
		return Tokens{}, Session{}, ErrInvalid
	}
	now := s.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Tokens{}, Session{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var family string
	var expires, usedAt, succExp int64
	var succEnc []byte
	err = tx.QueryRowContext(ctx, `SELECT family_id, expires_at, used_at, successor_enc, successor_expires
		FROM refresh_tokens WHERE hash = ?`, hash(refresh)).Scan(&family, &expires, &usedAt, &succEnc, &succExp)
	if errors.Is(err, sql.ErrNoRows) {
		return Tokens{}, Session{}, ErrInvalid
	}
	if err != nil {
		return Tokens{}, Session{}, err
	}
	sess, revoked, err := scanFamily(tx.QueryRowContext(ctx, `SELECT `+familyCols+` FROM session_families f WHERE f.id = ?`, family))
	if err != nil {
		return Tokens{}, Session{}, err
	}
	if revoked != 0 || ms(now) >= expires || !now.Before(sess.AbsoluteExpiresAt) {
		return Tokens{}, Session{}, ErrInvalid
	}
	if usedAt != 0 {
		if ms(now) < succExp && len(succEnc) > 0 {
			var sc successor
			if plain, err := s.open(family, succEnc); err == nil && json.Unmarshal(plain, &sc) == nil {
				return Tokens{Access: sc.Access, Refresh: sc.Refresh, AccessExpires: sc.AccessExpires,
					AbsoluteExpires: sess.AbsoluteExpiresAt}, sess, nil
			}
		}
		if err := s.revokeTx(ctx, tx, family, "system", "refresh_reuse", now); err != nil {
			return Tokens{}, Session{}, err
		}
		if err := tx.Commit(); err != nil {
			return Tokens{}, Session{}, err
		}
		return Tokens{}, Session{}, ErrReuse
	}
	toks, err := s.mint(ctx, tx, family, now, sess.AbsoluteExpiresAt)
	if err != nil {
		return Tokens{}, Session{}, err
	}
	plain, _ := json.Marshal(successor{Access: toks.Access, Refresh: toks.Refresh, AccessExpires: toks.AccessExpires})
	res, err := tx.ExecContext(ctx, `UPDATE refresh_tokens SET used_at = ?, successor_enc = ?, successor_expires = ?
		WHERE hash = ? AND used_at = 0`, ms(now), s.seal(family, plain), ms(now.Add(RefreshGrace)), hash(refresh))
	if err != nil {
		return Tokens{}, Session{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return Tokens{}, Session{}, ErrInvalid
	}
	return toks, sess, tx.Commit()
}

func (s *Store) seal(family string, plain []byte) []byte {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		panic(err)
	}
	return s.aead.Seal(nonce, nonce, plain, []byte(family))
}

func (s *Store) open(family string, box []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(box) < n {
		return nil, errors.New("short ciphertext")
	}
	return s.aead.Open(nil, box[:n], box[n:], []byte(family))
}

func (s *Store) revokeTx(ctx context.Context, tx *sql.Tx, family, by, reason string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `UPDATE session_families SET revoked_at = ?, revoked_by = ?, revoke_reason = ?
		WHERE id = ? AND revoked_at = 0`, ms(now), by, reason, family); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM access_tokens WHERE family_id = ?`, family); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE family_id = ?`, family)
	return err
}

// Revoke ends one session family immediately.
func (s *Store) Revoke(ctx context.Context, family, by, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.revokeTx(ctx, tx, family, by, reason, s.Now()); err != nil {
		return err
	}
	return tx.Commit()
}

// RevokeWhere revokes every active family of userID, except keep (may be "").
func (s *Store) RevokeWhere(ctx context.Context, userID, keep, by, reason string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM session_families WHERE user_id = ? AND revoked_at = 0 AND id != ?`, userID, keep)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := s.Revoke(ctx, id, by, reason); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// List returns active families, for one user or (userID == "") everyone.
func (s *Store) List(ctx context.Context, userID string) ([]Session, error) {
	now := ms(s.Now())
	q := `SELECT ` + familyCols + ` FROM session_families f WHERE f.revoked_at = 0 AND f.absolute_expires_at > ?`
	args := []any{now}
	if userID != "" {
		q += ` AND f.user_id = ?`
		args = append(args, userID)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY f.last_seen_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		sess, _, err := scanFamily(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// Get returns an active family by id.
func (s *Store) Get(ctx context.Context, family string) (Session, error) {
	sess, revoked, err := scanFamily(s.db.QueryRowContext(ctx, `SELECT `+familyCols+` FROM session_families f WHERE f.id = ?`, family))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (revoked != 0 || !s.Now().Before(sess.AbsoluteExpiresAt))) {
		return Session{}, ErrInvalid
	}
	return sess, err
}

// MarkReauth records a successful password re-authentication and refreshes
// the role from Jellyfin.
func (s *Store) MarkReauth(ctx context.Context, family string, isAdmin bool) error {
	now := ms(s.Now())
	role := RoleUser
	if isAdmin {
		role = RoleAdmin
	}
	_, err := s.db.ExecContext(ctx, `UPDATE session_families SET reauth_at = ?, role_verified_at = ?, role = ?
		WHERE id = ? AND revoked_at = 0`, now, now, role, family)
	return err
}

// Prune deletes expired tokens and wipes expired successor ciphertexts.
func (s *Store) Prune(ctx context.Context) error {
	now := ms(s.Now())
	stmts := []string{
		`DELETE FROM access_tokens WHERE expires_at <= ?`,
		`UPDATE refresh_tokens SET successor_enc = NULL WHERE successor_enc IS NOT NULL AND successor_expires <= ?`,
		`DELETE FROM refresh_tokens WHERE expires_at <= ?`,
	}
	for _, q := range stmts {
		if _, err := s.db.ExecContext(ctx, q, now); err != nil {
			return err
		}
	}
	return nil
}
