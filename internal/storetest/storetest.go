package storetest

import (
	"context"
	"os"
	"testing"

	"github.com/EvAvKein/Fortytwode/internal/store"
)

func OpenStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}
