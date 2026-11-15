package session

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ntsrihari/knowhere-nexus/internal/store"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func newStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "d"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := NewStore(db.DB, bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	s.Now = c.now
	return s, c
}

var ctx = context.Background()

func create(t *testing.T, s *Store, admin bool) (Tokens, Session) {
	t.Helper()
	tok, sess, err := s.Create(ctx, New{UserID: "u1", Username: "alice", IsAdmin: admin, Kind: KindBrowser, AuthMethod: "password"})
	if err != nil {
		t.Fatal(err)
	}
	return tok, sess
}

func TestCreateAuthenticateExpire(t *testing.T) {
	s, c := newStore(t)
	tok, sess := create(t, s, true)
	got, err := s.Authenticate(ctx, tok.Access)
	if err != nil || got.FamilyID != sess.FamilyID || !got.IsAdmin(c.now()) {
		t.Fatalf("authenticate: %+v %v", got, err)
	}
	if _, err := s.Authenticate(ctx, tok.Refresh); err == nil {
		t.Fatal("refresh token accepted as access token")
	}
	c.add(AccessTTL)
	if _, err := s.Authenticate(ctx, tok.Access); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expired access accepted: %v", err)
	}
}

func TestRefreshRotation(t *testing.T) {
	s, c := newStore(t)
	tok, _ := create(t, s, false)
	c.add(10 * time.Minute)
	t2, _, err := s.Refresh(ctx, tok.Refresh)
	if err != nil || t2.Access == tok.Access || t2.Refresh == tok.Refresh {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := s.Authenticate(ctx, t2.Access); err != nil {
		t.Fatal(err)
	}
	// Reuse after the grace window revokes the whole family.
	c.add(RefreshGrace + time.Second)
	if _, _, err := s.Refresh(ctx, tok.Refresh); !errors.Is(err, ErrReuse) {
		t.Fatalf("reuse: %v", err)
	}
	if _, err := s.Authenticate(ctx, t2.Access); !errors.Is(err, ErrInvalid) {
		t.Fatal("family not revoked after reuse")
	}
	if _, _, err := s.Refresh(ctx, t2.Refresh); err == nil {
		t.Fatal("successor refresh still valid after reuse")
	}
}

func TestConcurrentRefreshSameSuccessor(t *testing.T) {
	s, _ := newStore(t)
	tok, _ := create(t, s, false)
	const n = 10
	var wg sync.WaitGroup
	res := make([]Tokens, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); res[i], _, errs[i] = s.Refresh(ctx, tok.Refresh) }(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("refresh %d: %v", i, errs[i])
		}
		if res[i].Access != res[0].Access || res[i].Refresh != res[0].Refresh {
			t.Fatalf("refresh %d got a different successor", i)
		}
	}
	if _, err := s.Authenticate(ctx, res[0].Access); err != nil {
		t.Fatalf("family revoked by concurrent refresh: %v", err)
	}
}

func TestLostResponseRetryWithinGrace(t *testing.T) {
	s, c := newStore(t)
	tok, _ := create(t, s, false)
	first, _, err := s.Refresh(ctx, tok.Refresh)
	if err != nil {
		t.Fatal(err)
	}
	c.add(RefreshGrace - time.Second)
	again, _, err := s.Refresh(ctx, tok.Refresh)
	if err != nil || again.Refresh != first.Refresh {
		t.Fatalf("retry within grace: %v", err)
	}
}

func TestAbsoluteCapCannotBeExceeded(t *testing.T) {
	s, c := newStore(t)
	tok, sess := create(t, s, false)
	cur := tok
	for i := 0; ; i++ {
		c.add(14 * time.Minute)
		next, _, err := s.Refresh(ctx, cur.Refresh)
		if err != nil {
			if !c.now().Before(sess.AbsoluteExpiresAt.Add(-14 * time.Minute)) {
				break // ended at the cap, as required
			}
			t.Fatalf("refresh %d failed early: %v", i, err)
		}
		if next.AbsoluteExpires != sess.AbsoluteExpiresAt || next.AccessExpires.After(sess.AbsoluteExpiresAt) {
			t.Fatal("refresh extended the absolute expiry")
		}
		cur = next
	}
	if c.now().Sub(sess.CreatedAt) > AbsoluteTTL+15*time.Minute {
		t.Fatal("session outlived the 7-day cap")
	}
	if _, err := s.Authenticate(ctx, cur.Access); err == nil {
		c.add(15 * time.Minute)
		if _, err := s.Authenticate(ctx, cur.Access); err == nil {
			t.Fatal("access valid after the cap")
		}
	}
}

func TestRevokeAndList(t *testing.T) {
	s, _ := newStore(t)
	a, sa := create(t, s, false)
	b, sb := create(t, s, false)
	list, err := s.List(ctx, "u1")
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %d %v", len(list), err)
	}
	if err := s.Revoke(ctx, sa.FamilyID, "u1", "logout"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, a.Access); err == nil {
		t.Fatal("revoked access accepted")
	}
	if _, _, err := s.Refresh(ctx, a.Refresh); err == nil {
		t.Fatal("revoked refresh accepted")
	}
	ids, err := s.RevokeWhere(ctx, "u1", "", "admin", "admin_revoke")
	if err != nil || len(ids) != 1 || ids[0] != sb.FamilyID {
		t.Fatalf("revoke where: %v %v", ids, err)
	}
	if _, err := s.Authenticate(ctx, b.Access); err == nil {
		t.Fatal("revoke-all left a session")
	}
}

func TestAdminWindowAndReauth(t *testing.T) {
	s, c := newStore(t)
	tok, _ := create(t, s, true)
	sess, _ := s.Authenticate(ctx, tok.Access)
	if !sess.IsAdmin(c.now()) || sess.RecentlyReauthenticated(c.now()) {
		t.Fatal("fresh admin state wrong")
	}
	if sess.IsAdmin(c.now().Add(AdminWindow)) {
		t.Fatal("admin honoured after 12 h")
	}
	if err := s.MarkReauth(ctx, sess.FamilyID, true); err != nil {
		t.Fatal(err)
	}
	c.add(time.Minute)
	sess, _ = s.Authenticate(ctx, tok.Access)
	if !sess.RecentlyReauthenticated(c.now()) {
		t.Fatal("reauth not recorded")
	}
	if err := s.MarkReauth(ctx, sess.FamilyID, false); err != nil {
		t.Fatal(err)
	}
	sess, _ = s.Authenticate(ctx, tok.Access)
	if sess.IsAdmin(c.now()) {
		t.Fatal("demotion at reauth not applied")
	}
}

func TestPruneWipesSuccessors(t *testing.T) {
	s, c := newStore(t)
	tok, _ := create(t, s, false)
	if _, _, err := s.Refresh(ctx, tok.Refresh); err != nil {
		t.Fatal(err)
	}
	c.add(RefreshGrace + time.Second)
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM refresh_tokens WHERE successor_enc IS NOT NULL`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("successor ciphertext kept: %d %v", n, err)
	}
}
