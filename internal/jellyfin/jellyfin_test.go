package jellyfin

import (
	"context"
	"errors"
	"testing"

	"github.com/ntsrihari/knowhere-nexus/internal/testutil"
	"github.com/ntsrihari/knowhere-nexus/internal/upstream"
)

func setup(t *testing.T) (*testutil.FakeJellyfin, *Client) {
	t.Helper()
	f := testutil.NewFakeJellyfin(
		testutil.FakeUser{ID: "u1", Name: "alice", Password: "pw", Admin: true},
		testutil.FakeUser{ID: "u2", Name: "bob", Password: "pw2"},
		testutil.FakeUser{ID: "u3", Name: "carol", Password: "pw3", Disabled: true},
	)
	t.Cleanup(f.Close)
	return f, New(upstream.New("jellyfin", f.URL))
}

func TestLoginDiscardsToken(t *testing.T) {
	f, c := setup(t)
	id, err := c.Login(context.Background(), "alice", "pw")
	if err != nil || id.UserID != "u1" || !id.IsAdmin {
		t.Fatalf("login: %+v %v", id, err)
	}
	id, err = c.Login(context.Background(), "bob", "pw2")
	if err != nil || id.IsAdmin {
		t.Fatalf("bob: %+v %v", id, err)
	}
	if f.ActiveTokens() != 0 || len(f.LoggedOut) != 2 {
		t.Fatalf("tokens not discarded: active=%d loggedOut=%d", f.ActiveTokens(), len(f.LoggedOut))
	}
}

func TestLoginFailures(t *testing.T) {
	f, c := setup(t)
	for _, tc := range [][2]string{{"alice", "wrong"}, {"nobody", "x"}, {"carol", "pw3"}} {
		if _, err := c.Login(context.Background(), tc[0], tc[1]); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("%v: %v", tc, err)
		}
	}
	if f.ActiveTokens() != 0 {
		t.Fatal("disabled user's token left active")
	}
	f.Close()
	if _, err := c.Login(context.Background(), "alice", "pw"); err == nil || errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unreachable server should be an upstream error, got %v", err)
	}
}

func TestQuickConnect(t *testing.T) {
	f, c := setup(t)
	qc, err := c.StartQuickConnect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := c.PollQuickConnect(context.Background(), qc); ok || err != nil {
		t.Fatalf("pending: ok=%v err=%v", ok, err)
	}
	if !f.ApproveQuickConnect(qc.Code, "bob") {
		t.Fatal("approve failed")
	}
	id, ok, err := c.PollQuickConnect(context.Background(), qc)
	if err != nil || !ok || id.UserID != "u2" {
		t.Fatalf("approved: %+v %v %v", id, ok, err)
	}
	if f.ActiveTokens() != 0 {
		t.Fatal("quick connect token not discarded")
	}
}

func TestValidate(t *testing.T) {
	f, c := setup(t)
	tok := f.IssueToken("alice")
	id, err := c.Validate(context.Background(), tok)
	if err != nil || id.UserID != "u1" || !id.IsAdmin {
		t.Fatalf("%+v %v", id, err)
	}
	if _, err := c.Validate(context.Background(), "bogus"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("bogus: %v", err)
	}
	if f.ActiveTokens() != 1 {
		t.Fatal("validate must not log out the Milano token")
	}
}
