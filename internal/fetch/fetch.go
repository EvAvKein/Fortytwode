// Package fetch orchestrates a full pull of one user's 42 data. Pull is the
// reusable core (returns the snapshot, emits progress); Run is the CLI flow that
// authenticates and writes the snapshot to ./output as JSON files.
package fetch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EvAvKein/Fortytwode/internal/api42"
	"github.com/EvAvKein/Fortytwode/internal/auth"
	"github.com/EvAvKein/Fortytwode/internal/config"
	"github.com/EvAvKein/Fortytwode/internal/snapshot"
)

const outputDir = "output"

// meHeader is the slice of /v2/me we need: the id keys the sub-resource requests
// and identifies the user, and titles_users is grabbed here because its
// standalone endpoint is role-gated.
type meHeader struct {
	ID          int               `json:"id"`
	Login       string            `json:"login"`
	TitlesUsers []json.RawMessage `json:"titles_users"`
}

// Result is a completed pull: the snapshot (resource -> payload) plus the 42
// identity it belongs to.
type Result struct {
	Snapshot map[string]json.RawMessage
	FtID     int64
	FtLogin  string
}

// Pull fetches /me, titles_users, and every personal collection, returning them
// as one snapshot. progress (if non-nil) is called once per step before it runs,
// with the 0-based count of completed steps, the total, and the step's name. A
// single failing collection is skipped rather than aborting the whole pull.
//
// afterMe (if non-nil) is called with the 42 user id as soon as /me identifies
// the user, before any further requests; a non-nil error from it aborts the pull
// (used by the web server to enforce the per-user sync cooldown cheaply).
func Pull(ctx context.Context, client *api42.Client, progress func(step, total int, name string), afterMe func(ftID int64) error) (Result, error) {
	total := 2 + len(api42.Collections) // me, titles_users, + each collection
	step := 0
	emit := func(name string) {
		if progress != nil {
			progress(step, total, name)
		}
		step++
	}

	snapshot := map[string]json.RawMessage{}

	emit("profile")
	rawMe, err := client.GetOne(ctx, "me")
	if err != nil {
		return Result{}, err
	}
	snapshot["me"] = rawMe

	var me meHeader
	if err := json.Unmarshal(rawMe, &me); err != nil {
		return Result{}, err
	}
	if me.ID == 0 {
		return Result{}, fmt.Errorf("could not read user id from /v2/me response")
	}

	if afterMe != nil {
		if err := afterMe(int64(me.ID)); err != nil {
			return Result{}, err
		}
	}

	emit("titles")
	titles, err := json.Marshal(me.TitlesUsers)
	if err != nil {
		return Result{}, err
	}
	snapshot["titles_users"] = titles

	for _, ep := range api42.Collections {
		emit(ep.File)
		records, err := client.GetAll(ctx, fmt.Sprintf("users/%d/%s", me.ID, ep.Suffix))
		if err != nil {
			fmt.Printf("  skipped %s (%v)\n", ep.File, err)
			continue
		}
		raw, err := json.Marshal(records)
		if err != nil {
			return Result{}, err
		}
		snapshot[ep.File] = raw
	}

	return Result{Snapshot: snapshot, FtID: int64(me.ID), FtLogin: me.Login}, nil
}

// pull authenticates (caching the token in .token.json) and runs a full pull,
// printing per-step progress. Shared by the Run and RunCurated CLI flows.
func pull(ctx context.Context, cfg config.Config) (Result, error) {
	token, err := auth.AccessToken(cfg)
	if err != nil {
		return Result{}, err
	}
	client := api42.New(token, nil) // solo limiter: single user
	return Pull(ctx, client, func(step, total int, name string) {
		fmt.Printf("[%d/%d] %s ...\n", step+1, total, name)
	}, nil)
}

// Run is the CLI flow: pull and write the raw snapshot to ./output/<resource>.json.
func Run(ctx context.Context, cfg config.Config) error {
	res, err := pull(ctx, cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}
	for name, raw := range res.Snapshot {
		if err := writeJSON(name, raw); err != nil {
			return err
		}
	}
	fmt.Printf("\nDone. Wrote %d files to ./%s/ for %s (id %d).\n", len(res.Snapshot), outputDir, res.FtLogin, res.FtID)
	return nil
}

// RunCurated pulls and writes only the single curated snapshot we would persist, to
// ./output/curated.json — a transparency preview of exactly what the database stores
// (third-party identities stripped), produced by the same snapshot.Curate as the
// web app's persistence path.
func RunCurated(ctx context.Context, cfg config.Config) error {
	res, err := pull(ctx, cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}
	blob, err := json.MarshalIndent(snapshot.Curate(res.Snapshot), "", "  ")
	if err != nil {
		return err
	}
	target := filepath.Join(outputDir, "curated.json")
	if err := os.WriteFile(target, append(blob, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("\nDone. Wrote ./%s/curated.json for %s (id %d).\n", outputDir, res.FtLogin, res.FtID)
	return nil
}

// writeJSON pretty-prints one resource to output/<name>.json.
func writeJSON(name string, raw json.RawMessage) error {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		pretty.Reset()
		pretty.Write(raw)
	}
	target := filepath.Join(outputDir, name+".json")
	return os.WriteFile(target, append(pretty.Bytes(), '\n'), 0o644)
}
