package audit

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ntsrihari/knowhere-nexus/internal/store"
)

func TestWriteListVerifyTamper(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "d"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	l := New(db.DB)
	for i, a := range []string{"login", "session.revoke", "theme.household.set"} {
		if err := l.Write(ctx, Entry{ActorUserID: "u1", ActorName: "alice", Action: a, Target: "t", Params: map[string]any{"i": i}, Result: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := l.List(ctx, 0, 10, "", "")
	if err != nil || len(got) != 3 || got[0].Action != "theme.household.set" || got[2].Params["i"] != float64(0) {
		t.Fatalf("list: %+v %v", got, err)
	}
	if f, _ := l.List(ctx, 0, 10, "", "login"); len(f) != 1 {
		t.Fatal("action filter")
	}
	if broken, err := l.Verify(ctx); err != nil || broken != 0 {
		t.Fatalf("verify: %d %v", broken, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE audit SET result = 'denied' WHERE seq = 2`); err != nil {
		t.Fatal(err)
	}
	if broken, _ := l.Verify(ctx); broken != 2 {
		t.Fatalf("tamper not detected: %d", broken)
	}
}
