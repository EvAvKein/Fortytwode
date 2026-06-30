package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvAvKein/Fortytwode/internal/store"
	"github.com/EvAvKein/Fortytwode/internal/storetest"
)

func TestAccountsAndSessions(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	ctx := context.Background()
	unique := uniqueID()
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
	t.Cleanup(func() { st.DeleteAccount(ctx, id) })

	// email, ft_id and ft_login are each independently UNIQUE, so each duplicate
	// is tested in isolation by colliding on one field while varying the others.
	// The non-colliding fields use a fresh uniqueID() (not ftID+1) so a parallel
	// test can't accidentally own that id and turn this into an ft_id collision.
	t.Run("DuplicateEmail", func(t *testing.T) {
		if _, err := st.CreateAccount(ctx, email, "h", uniqueID(), "other-"+login, data); !errors.Is(err, store.ErrDuplicate) {
			t.Errorf("got %v, want ErrDuplicate", err)
		}
	})
	t.Run("DuplicateFtID", func(t *testing.T) {
		if _, err := st.CreateAccount(ctx, "other-"+email, "h", ftID, "other-"+login, data); !errors.Is(err, store.ErrDuplicate) {
			t.Errorf("got %v, want ErrDuplicate", err)
		}
	})

	t.Run("AccountByEmail", func(t *testing.T) {
		if acc, hash, err := st.AccountByEmail(ctx, email); err != nil || hash != "hash$value" || acc.ID != id {
			t.Errorf("acc=%+v hash=%q err=%v", acc, hash, err)
		}
	})
	t.Run("AccountByLogin", func(t *testing.T) {
		if acc, err := st.AccountByLogin(ctx, login); err != nil || acc.ID != id {
			t.Errorf("%+v %v", acc, err)
		}
	})
	t.Run("AccountByFtID", func(t *testing.T) {
		if acc, err := st.AccountByFtID(ctx, ftID); err != nil || acc.ID != id {
			t.Errorf("%+v %v", acc, err)
		}
	})
	t.Run("MissingLogin", func(t *testing.T) {
		if _, err := st.AccountByLogin(ctx, "nobody-"+login); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("got %v, want ErrNotFound", err)
		}
	})

	t.Run("UpdateSnapshot", func(t *testing.T) {
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
	})

	t.Run("UpdateVisibility", func(t *testing.T) {
		if err := st.UpdateVisibility(ctx, id, true, map[string]bool{"locations": true}); err != nil {
			t.Fatalf("update visibility: %v", err)
		}
		if acc, _ := st.AccountByLogin(ctx, login); !acc.IsPublic || !acc.Visibility["locations"] {
			t.Errorf("visibility not saved: %+v", acc)
		}
	})

	// Sessions: create -> lookup -> delete; expired -> not found.
	sid := "sess-" + login
	t.Run("CreateSession", func(t *testing.T) {
		if err := st.CreateSession(ctx, sid, id, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("create session: %v", err)
		}
		if got, err := st.SessionAccount(ctx, sid); err != nil || got.ID != id {
			t.Errorf("SessionAccount: %+v %v", got, err)
		}
	})
	t.Run("DeleteSession", func(t *testing.T) {
		if err := st.DeleteSession(ctx, sid); err != nil {
			t.Fatalf("delete session: %v", err)
		}
		if _, err := st.SessionAccount(ctx, sid); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("deleted session: got %v, want ErrNotFound", err)
		}
	})
	t.Run("ExpiredSession", func(t *testing.T) {
		expired := "expired-" + login
		if err := st.CreateSession(ctx, expired, id, time.Now().Add(-time.Minute)); err != nil {
			t.Fatalf("create expired session: %v", err)
		}
		if _, err := st.SessionAccount(ctx, expired); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("expired session: got %v, want ErrNotFound", err)
		}
	})
}

func TestReserveSync(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	ctx := context.Background()
	ftID := uniqueID()
	t.Cleanup(func() { st.TestPool().Exec(ctx, `DELETE FROM sync_cooldowns WHERE ft_id=$1`, ftID) })

	cooldown := time.Hour

	// First reservation succeeds.
	if retry, ok, last, err := st.ReserveSync(ctx, ftID, cooldown); err != nil || !ok || retry != 0 || !last.IsZero() {
		t.Fatalf("first reserve: retry=%v ok=%v last=%v err=%v, want 0/true/zero/nil", retry, ok, last, err)
	}

	// A second within the cooldown is blocked, reporting a positive remaining time and the previous sync.
	retry, ok, last, err := st.ReserveSync(ctx, ftID, cooldown)
	if err != nil || ok {
		t.Fatalf("second reserve: ok=%v err=%v, want false/nil", ok, err)
	}
	if retry <= 0 || retry > cooldown {
		t.Errorf("retry-after = %v, want in (0, %v]", retry, cooldown)
	}
	if last.IsZero() {
		t.Errorf("blocked reserve should return the previous sync time")
	}

	// The read-only check agrees: active, with remaining time, and no slot claimed.
	if rem, active, last, err := st.SyncCooldown(ctx, ftID, cooldown); err != nil || !active || rem <= 0 || last.IsZero() {
		t.Errorf("SyncCooldown while cooling: rem=%v active=%v last=%v err=%v, want >0/true/nonzero/nil", rem, active, last, err)
	}

	// After release, both the check and a fresh reservation report free.
	if err := st.ReleaseSync(ctx, ftID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, active, last, err := st.SyncCooldown(ctx, ftID, cooldown); err != nil || active || !last.IsZero() {
		t.Errorf("SyncCooldown after release: active=%v last=%v err=%v, want false/zero/nil", active, last, err)
	}
	if _, ok, last, err := st.ReserveSync(ctx, ftID, cooldown); err != nil || !ok || !last.IsZero() {
		t.Fatalf("reserve after release: ok=%v last=%v err=%v, want true/zero/nil", ok, last, err)
	}
}

func TestDeleteAccount(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	ctx := context.Background()
	unique := uniqueID()
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
	if _, err := st.AccountByLogin(ctx, login); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("account after delete: got %v, want ErrNotFound", err)
	}
	if _, err := st.SessionAccount(ctx, sid); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("session should cascade away: got %v, want ErrNotFound", err)
	}
}

func TestPurgeStaleCooldowns(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	ctx := context.Background()
	ftID := uniqueID()
	t.Cleanup(func() { st.TestPool().Exec(ctx, `DELETE FROM sync_cooldowns WHERE ft_id=$1`, ftID) })

	if _, err := st.TestPool().Exec(ctx,
		`INSERT INTO sync_cooldowns (ft_id, last_sync_at) VALUES ($1, now() - interval '2 hours')`, ftID); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if n, err := st.PurgeStaleCooldowns(ctx, time.Hour); err != nil || n < 1 {
		t.Fatalf("purge: n=%d err=%v, want >=1/nil", n, err)
	}
	if _, active, _, err := st.SyncCooldown(ctx, ftID, time.Hour); err != nil || active {
		t.Errorf("stale row should be gone: active=%v err=%v", active, err)
	}
}

func TestPurgeExpiredSessions(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	ctx := context.Background()
	unique := uniqueID()
	login := fmt.Sprintf("purge%d", unique)
	data := map[string]json.RawMessage{"me": json.RawMessage(`{"login":"` + login + `"}`)}

	id, err := st.CreateAccount(ctx, fmt.Sprintf("purge-%d@e.st", unique), "h", unique, login, data)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { st.DeleteAccount(ctx, id) })

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
	if err := st.TestPool().QueryRow(ctx, `SELECT count(*) FROM sessions WHERE id=$1`, expired).Scan(&count); err != nil || count != 0 {
		t.Errorf("expired session row: count=%d err=%v, want 0/nil", count, err)
	}
	// ...and the live one survives.
	if got, err := st.SessionAccount(ctx, live); err != nil || got.ID != id {
		t.Errorf("live session after purge: %+v %v", got, err)
	}
}

func TestAccountCredentialsAndSessions(t *testing.T) {
	t.Parallel()
	st := storetest.OpenStore(t)
	ctx := context.Background()
	unique := uniqueID()
	email := fmt.Sprintf("cred-%d@e.st", unique)
	login := fmt.Sprintf("cred%d", unique)
	ftID := unique

	id, err := st.CreateAccount(ctx, email, "hash$value", ftID, login, map[string]json.RawMessage{
		"me": json.RawMessage(`{"login":"` + login + `"}`),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { st.DeleteAccount(ctx, id) })

	// UpdateEmail changes the address and reports duplicates.
	newEmail := fmt.Sprintf("new-%d@e.st", unique)
	if err := st.UpdateEmail(ctx, id, newEmail); err != nil {
		t.Fatalf("update email: %v", err)
	}
	if acc, _, err := st.AccountByEmail(ctx, newEmail); err != nil || acc.Email != newEmail {
		t.Errorf("lookup new email: %+v %v", acc, err)
	}

	otherLogin := fmt.Sprintf("other%d", unique)
	otherID, err := st.CreateAccount(ctx, email, "h", uniqueID(), otherLogin, map[string]json.RawMessage{
		"me": json.RawMessage(`{"login":"` + otherLogin + `"}`),
	})
	if err != nil {
		t.Fatalf("create other account: %v", err)
	}
	t.Cleanup(func() { st.DeleteAccount(ctx, otherID) })
	if err := st.UpdateEmail(ctx, id, email); !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("duplicate email update: got %v, want ErrDuplicate", err)
	}

	// AccountPasswordHash returns the stored hash.
	if hash, err := st.AccountPasswordHash(ctx, id); err != nil || hash != "hash$value" {
		t.Errorf("password hash: got %q %v", hash, err)
	}
	if _, err := st.AccountPasswordHash(ctx, 0); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing account hash: got %v, want ErrNotFound", err)
	}

	// UpdatePassword changes the hash.
	if err := st.UpdatePassword(ctx, id, "newhash"); err != nil {
		t.Fatalf("update password: %v", err)
	}
	if hash, err := st.AccountPasswordHash(ctx, id); err != nil || hash != "newhash" {
		t.Errorf("updated password hash: got %q %v", hash, err)
	}

	// DeleteOtherSessions keeps the current session and removes the rest.
	current := "current-" + login
	other := "other-" + login
	if err := st.CreateSession(ctx, current, id, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create current session: %v", err)
	}
	if err := st.CreateSession(ctx, other, id, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create other session: %v", err)
	}
	if err := st.DeleteOtherSessions(ctx, id, current); err != nil {
		t.Fatalf("delete other sessions: %v", err)
	}
	if got, err := st.SessionAccount(ctx, current); err != nil || got.ID != id {
		t.Errorf("current session should survive: %+v %v", got, err)
	}
	if _, err := st.SessionAccount(ctx, other); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("other session should be deleted: got %v, want ErrNotFound", err)
	}
}

var (
	testIDBase = time.Now().UnixNano()
	testIDSeq  atomic.Int64
)

// uniqueID returns an int64 unique to this call. The per-process base keeps it
// distinct from rows any earlier test run may have left behind, while the atomic
// counter guarantees no two calls collide — needed now that the tests run with
// t.Parallel() and a coarse clock could otherwise hand out duplicate seeds.
func uniqueID() int64 { return testIDBase + testIDSeq.Add(1) }

func keys(m map[string]json.RawMessage) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
