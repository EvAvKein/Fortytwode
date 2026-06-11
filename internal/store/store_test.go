package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

// testStore opens the database named by DATABASE_URL, skipping the test when it
// is unset (so `go test ./...` stays hermetic without Postgres).
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("set DATABASE_URL to run store integration tests")
	}
	st, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

func TestAccountsAndSessions(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	unique := time.Now().UnixNano()
	email := fmt.Sprintf("user-%d@e.st", unique)
	login := fmt.Sprintf("tester%d", unique)
	ftID := unique

	data := map[string]json.RawMessage{
		"me":             json.RawMessage(`{"login":"` + login + `"}`),
		"projects_users": json.RawMessage(`[{"project":{"name":"libft"}}]`),
	}
	id, err := st.CreateAccount(ctx, email, "hash$value", ftID, login, data)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { st.pool.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, id) })

	// Duplicate email/ft_id is reported as ErrDuplicate.
	if _, err := st.CreateAccount(ctx, email, "h", ftID, login, data); !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate create: got %v, want ErrDuplicate", err)
	}

	// Lookups.
	if acc, hash, err := st.AccountByEmail(ctx, email); err != nil || hash != "hash$value" || acc.ID != id {
		t.Errorf("AccountByEmail: acc=%+v hash=%q err=%v", acc, hash, err)
	}
	if acc, err := st.AccountByLogin(ctx, login); err != nil || acc.ID != id {
		t.Errorf("AccountByLogin: %+v %v", acc, err)
	}
	if acc, err := st.AccountByFtID(ctx, ftID); err != nil || acc.ID != id {
		t.Errorf("AccountByFtID: %+v %v", acc, err)
	}
	if _, err := st.AccountByLogin(ctx, "nobody-"+login); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing login: got %v, want ErrNotFound", err)
	}

	// Merge update preserves resources absent from the new data.
	if err := st.UpdateSnapshot(ctx, id, map[string]json.RawMessage{"locations": json.RawMessage(`[{"host":"c1"}]`)}); err != nil {
		t.Fatalf("update snapshot: %v", err)
	}
	acc, err := st.AccountByLogin(ctx, login)
	if err != nil {
		t.Fatal(err)
	}
	if acc.Data["projects_users"] == nil || acc.Data["locations"] == nil {
		t.Errorf("merge lost a resource: keys=%v", keys(acc.Data))
	}

	// Visibility.
	if err := st.UpdateVisibility(ctx, id, true, map[string]bool{"locations": true}); err != nil {
		t.Fatalf("update visibility: %v", err)
	}
	if acc, _ := st.AccountByLogin(ctx, login); !acc.IsPublic || !acc.Visibility["locations"] {
		t.Errorf("visibility not saved: %+v", acc)
	}

	// Sessions: create -> lookup -> delete; expired -> not found.
	sid := "sess-" + login
	if err := st.CreateSession(ctx, sid, id, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if got, err := st.SessionAccount(ctx, sid); err != nil || got.ID != id {
		t.Errorf("SessionAccount: %+v %v", got, err)
	}
	if err := st.DeleteSession(ctx, sid); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := st.SessionAccount(ctx, sid); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted session: got %v, want ErrNotFound", err)
	}

	expired := "expired-" + login
	if err := st.CreateSession(ctx, expired, id, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("create expired session: %v", err)
	}
	if _, err := st.SessionAccount(ctx, expired); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired session: got %v, want ErrNotFound", err)
	}
}

func TestReserveSync(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	ftID := time.Now().UnixNano()
	t.Cleanup(func() { st.pool.Exec(ctx, `DELETE FROM sync_cooldowns WHERE ft_id=$1`, ftID) })

	cooldown := time.Hour

	// First reservation succeeds.
	if retry, ok, err := st.ReserveSync(ctx, ftID, cooldown); err != nil || !ok || retry != 0 {
		t.Fatalf("first reserve: retry=%v ok=%v err=%v, want 0/true/nil", retry, ok, err)
	}

	// A second within the cooldown is blocked, reporting a positive remaining time.
	retry, ok, err := st.ReserveSync(ctx, ftID, cooldown)
	if err != nil || ok {
		t.Fatalf("second reserve: ok=%v err=%v, want false/nil", ok, err)
	}
	if retry <= 0 || retry > cooldown {
		t.Errorf("retry-after = %v, want in (0, %v]", retry, cooldown)
	}

	// The read-only check agrees: active, with remaining time, and no slot claimed.
	if rem, active, err := st.SyncCooldown(ctx, ftID, cooldown); err != nil || !active || rem <= 0 {
		t.Errorf("SyncCooldown while cooling: rem=%v active=%v err=%v, want >0/true/nil", rem, active, err)
	}

	// After release, both the check and a fresh reservation report free.
	if err := st.ReleaseSync(ctx, ftID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, active, err := st.SyncCooldown(ctx, ftID, cooldown); err != nil || active {
		t.Errorf("SyncCooldown after release: active=%v err=%v, want false/nil", active, err)
	}
	if _, ok, err := st.ReserveSync(ctx, ftID, cooldown); err != nil || !ok {
		t.Fatalf("reserve after release: ok=%v err=%v, want true/nil", ok, err)
	}
}

func TestDeleteAccount(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	unique := time.Now().UnixNano()
	login := fmt.Sprintf("del%d", unique)
	data := map[string]json.RawMessage{"me": json.RawMessage(`{"login":"` + login + `"}`)}

	id, err := st.CreateAccount(ctx, fmt.Sprintf("del-%d@e.st", unique), "h", unique, login, data)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sid := "sd-" + login
	if err := st.CreateSession(ctx, sid, id, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := st.DeleteAccount(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.AccountByLogin(ctx, login); !errors.Is(err, ErrNotFound) {
		t.Errorf("account after delete: got %v, want ErrNotFound", err)
	}
	if _, err := st.SessionAccount(ctx, sid); !errors.Is(err, ErrNotFound) {
		t.Errorf("session should cascade away: got %v, want ErrNotFound", err)
	}
}

func TestPurgeStaleCooldowns(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	ftID := time.Now().UnixNano()
	t.Cleanup(func() { st.pool.Exec(ctx, `DELETE FROM sync_cooldowns WHERE ft_id=$1`, ftID) })

	if _, err := st.pool.Exec(ctx,
		`INSERT INTO sync_cooldowns (ft_id, last_sync_at) VALUES ($1, now() - interval '2 hours')`, ftID); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if n, err := st.PurgeStaleCooldowns(ctx, time.Hour); err != nil || n < 1 {
		t.Fatalf("purge: n=%d err=%v, want >=1/nil", n, err)
	}
	if _, active, err := st.SyncCooldown(ctx, ftID, time.Hour); err != nil || active {
		t.Errorf("stale row should be gone: active=%v err=%v", active, err)
	}
}

func TestPurgeExpiredSessions(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	unique := time.Now().UnixNano()
	login := fmt.Sprintf("purge%d", unique)
	data := map[string]json.RawMessage{"me": json.RawMessage(`{"login":"` + login + `"}`)}

	id, err := st.CreateAccount(ctx, fmt.Sprintf("purge-%d@e.st", unique), "h", unique, login, data)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { st.pool.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, id) })

	expired, live := "pexp-"+login, "plive-"+login
	if err := st.CreateSession(ctx, expired, id, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("create expired session: %v", err)
	}
	if err := st.CreateSession(ctx, live, id, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create live session: %v", err)
	}

	if n, err := st.PurgeExpiredSessions(ctx); err != nil || n < 1 {
		t.Fatalf("purge: n=%d err=%v, want >=1/nil", n, err)
	}

	// The expired row is gone outright (not just filtered by the lookup)...
	var count int
	if err := st.pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE id=$1`, expired).Scan(&count); err != nil || count != 0 {
		t.Errorf("expired session row: count=%d err=%v, want 0/nil", count, err)
	}
	// ...and the live one survives.
	if got, err := st.SessionAccount(ctx, live); err != nil || got.ID != id {
		t.Errorf("live session after purge: %+v %v", got, err)
	}
}

func keys(m map[string]json.RawMessage) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
