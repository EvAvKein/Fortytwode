package snapshot

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A raw scale_team carrying third-party identities (corrector, corrected
// students, feedback author) alongside the content we keep. project_id 1337 is
// resolved to a name via the projects_users below.
const rawEval = `[{
	"begin_at": "2026-05-29T10:00:00Z",
	"comment": "Solid defense, good answers.",
	"final_mark": 105,
	"flag": {"name": "Outstanding project", "positive": true},
	"corrector": {"login": "corrector", "id": 1},
	"correcteds": [{"login": "owner", "id": 2}, {"login": "student", "id": 3}],
	"truant": {"login": "no-show", "id": 99},
	"feedbacks": [{"user": {"login": "owner", "id": 2}, "rating": 5, "comment": "Very thorough."}],
	"scale": {"guidelines_md": "secret guidelines", "languages": [{"name": "C"}]},
	"team": {"name": "student's group", "project_id": 1337, "users": [{"login": "student", "id": 3}]}
}]`

const rawEvalProjects = `[{"project": {"id": 1337, "name": "ft_transcendence"}}]`

func TestCurateStripsThirdPartyIdentities(t *testing.T) {
	t.Parallel()
	out := Curate(map[string]json.RawMessage{
		"scale_teams_as_corrected": json.RawMessage(rawEval),
		"projects_users":           json.RawMessage(rawEvalProjects),
	})

	got := string(out["scale_teams_as_corrected"])
	for _, login := range []string{"corrector", "student", "no-show", "owner"} {
		if strings.Contains(got, login) {
			t.Errorf("curated eval leaked a login %q: %s", login, got)
		}
	}
	if strings.Contains(got, "guidelines") || strings.Contains(got, "secret") {
		t.Errorf("curated eval kept scale guidelines: %s", got)
	}

	var evals []Eval
	if err := json.Unmarshal(out["scale_teams_as_corrected"], &evals); err != nil {
		t.Fatalf("unmarshal curated: %v", err)
	}
	if len(evals) != 1 {
		t.Fatalf("got %d evals, want 1", len(evals))
	}
	e := evals[0]
	// The project name is resolved from the team's project_id.
	if e.Project != "ft_transcendence" {
		t.Errorf("project should resolve to ft_transcendence, got %q", e.Project)
	}
	// The default solo-project name ("student's group") is generic and dropped, not
	// stored — so it can't leak the login either way.
	if e.Team != "" {
		t.Errorf("generic team name should be dropped, got %q", e.Team)
	}
	if e.FlagName != "Outstanding project" || e.Comment != "Solid defense, good answers." {
		t.Errorf("dropped kept content: %+v", e)
	}
	// No "me" in this snapshot -> ownerLogin is unknown, so even the owner's own
	// feedback entry keeps no author.
	if len(e.Feedbacks) != 1 || e.Feedbacks[0].Author != "" || e.Feedbacks[0].Rating == nil ||
		*e.Feedbacks[0].Rating != 5 || e.Feedbacks[0].Comment != "Very thorough." {
		t.Errorf("dropped feedback rating/comment: %+v", e)
	}
	if !e.Truant {
		t.Error("truancy fact (no-show id 99) should be recorded")
	}
}

// Every feedback entry is kept in API order; only the owner's authorship survives —
// a teammate's entry keeps its rating/comment but loses its author, and the
// teammate's login is scrubbed from all kept text.
func TestCurateKeepsAllFeedbacks(t *testing.T) {
	t.Parallel()
	raw := `[{"begin_at": "x", "flag": {}, "team": {"project_id": 1}, "feedbacks": [
		{"user": {"login": "mate", "id": 3}, "rating": 2, "comment": "Meh, mate agrees."},
		{"user": {"login": "owner", "id": 2}, "rating": 5, "comment": "Great eval."}
	]}]`
	out := Curate(map[string]json.RawMessage{
		"me":                       json.RawMessage(`{"login": "owner", "id": 2}`),
		"scale_teams_as_corrected": json.RawMessage(raw),
	})
	var evals []Eval
	if err := json.Unmarshal(out["scale_teams_as_corrected"], &evals); err != nil {
		t.Fatalf("unmarshal curated: %v", err)
	}
	fbs := evals[0].Feedbacks
	if len(fbs) != 2 {
		t.Fatalf("got %d feedbacks, want 2", len(fbs))
	}
	if fbs[0].Author != "" || fbs[0].Rating == nil || *fbs[0].Rating != 2 || fbs[0].Comment != "Meh, [redacted] agrees." {
		t.Errorf("teammate entry should keep content but lose author (login scrubbed): %+v", fbs[0])
	}
	if fbs[1].Author != "owner" || fbs[1].Rating == nil || *fbs[1].Rating != 5 || fbs[1].Comment != "Great eval." {
		t.Errorf("owner entry should keep authorship: %+v", fbs[1])
	}
	if evals[0].Rating != nil || evals[0].FeedbackComment != "" {
		t.Errorf("legacy single-entry fields should no longer be written: %+v", evals[0])
	}
}

// AllFeedbacks bridges snapshots persisted before Feedbacks existed: the legacy
// rating/comment pair surfaces as one author-unknown entry, absent legacy ratings
// stay nil (distinct from a real 0-star rating), and evals without any feedback
// yield nothing.
func TestEvalAllFeedbacksLegacy(t *testing.T) {
	t.Parallel()
	rating := 0
	legacy := Eval{Rating: &rating, FeedbackComment: "Old text."}
	if fbs := legacy.AllFeedbacks(); len(fbs) != 1 || fbs[0].Author != "" || fbs[0].Rating != &rating || fbs[0].Comment != "Old text." {
		t.Errorf("legacy pair should surface as one author-unknown entry: %+v", fbs)
	}
	textOnly := Eval{FeedbackComment: "Old text."}
	if fbs := textOnly.AllFeedbacks(); len(fbs) != 1 || fbs[0].Rating != nil {
		t.Errorf("legacy entry without a rating should keep it nil: %+v", fbs)
	}
	if fbs := (Eval{}).AllFeedbacks(); fbs != nil {
		t.Errorf("eval without feedback should yield nothing, got %+v", fbs)
	}
}

// TestCurateKeepsDistinctiveTeamName checks the keep/drop split: a real team name
// survives, while default-style "<x>'s group"/"<x>'s team" names (which embed
// identities) are dropped regardless of capitalisation.
func TestCurateKeepsDistinctiveTeamName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want string
	}{
		{"Bop it, socket, diff it", "Bop it, socket, diff it"},
		{"student's group-2", ""},
		{"Foo and Bar's Team :D", ""},
	}
	for _, tc := range cases {
		raw := `[{"begin_at": "x", "flag": {}, "team": {"name": ` +
			mustJSON(tc.name) + `, "project_id": 1}}]`
		out := Curate(map[string]json.RawMessage{"scale_teams_as_corrector": json.RawMessage(raw)})
		var evals []Eval
		if err := json.Unmarshal(out["scale_teams_as_corrector"], &evals); err != nil {
			t.Fatalf("unmarshal curated: %v", err)
		}
		if evals[0].Team != tc.want {
			t.Errorf("team %q: got %q, want %q", tc.name, evals[0].Team, tc.want)
		}
	}
}

func TestCurateResolvesEvalProjectName(t *testing.T) {
	t.Parallel()
	// projects_users supplies the display name for projects the owner enrolled in;
	// evals on projects the owner only corrected fall back to the gitlab path slug.
	cases := []struct {
		name       string
		projectID  int
		gitlabPath string // empty -> field omitted
		projects   string // projects_users JSON, empty -> key omitted
		want       string
	}{
		{
			name:       "map hit wins over gitlab path",
			projectID:  1,
			gitlabPath: "pedago_world/42-cursus/inner-circle/minitalk",
			projects:   `[{"project": {"id": 1, "name": "CPP Module 09"}}]`,
			want:       "CPP Module 09",
		},
		{
			name:       "map miss falls back to gitlab slug",
			projectID:  2005,
			gitlabPath: "pedago_world/42-cursus/inner-circle/minitalk",
			projects:   `[{"project": {"id": 1, "name": "CPP Module 09"}}]`,
			want:       "minitalk",
		},
		{
			name:      "map miss and no gitlab path stays empty",
			projectID: 2005,
			want:      "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			team := `"project_id": ` + strconv.Itoa(tc.projectID)
			if tc.gitlabPath != "" {
				team += `, "project_gitlab_path": ` + mustJSON(tc.gitlabPath)
			}
			raw := map[string]json.RawMessage{
				"scale_teams_as_corrector": json.RawMessage(
					`[{"begin_at": "x", "flag": {}, "team": {` + team + `}}]`),
			}
			if tc.projects != "" {
				raw["projects_users"] = json.RawMessage(tc.projects)
			}
			out := Curate(raw)
			var evals []Eval
			if err := json.Unmarshal(out["scale_teams_as_corrector"], &evals); err != nil {
				t.Fatalf("unmarshal curated: %v", err)
			}
			if evals[0].Project != tc.want {
				t.Errorf("project: got %q, want %q", evals[0].Project, tc.want)
			}
		})
	}
}

func TestCuratePreservesEvalProjectPath(t *testing.T) {
	t.Parallel()
	// Curate keeps the raw gitlab path verbatim (the authoritative piscine signal); the
	// classification itself happens at render, so no pool metadata is needed here.
	cases := []struct {
		name       string
		gitlabPath string // empty -> field omitted
		want       string
	}{
		{"piscine path preserved", "pedago_world/c-piscine/c-piscine-c-00", "pedago_world/c-piscine/c-piscine-c-00"},
		{"cursus path preserved", "pedago_world/42-cursus/inner-circle/minitalk", "pedago_world/42-cursus/inner-circle/minitalk"},
		{"absent path stays empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			team := `"project_id": 1`
			if tc.gitlabPath != "" {
				team += `, "project_gitlab_path": ` + mustJSON(tc.gitlabPath)
			}
			raw := `[{"begin_at": "x", "flag": {}, "team": {` + team + `}}]`
			in := map[string]json.RawMessage{"scale_teams_as_corrector": json.RawMessage(raw)}
			out := Curate(in)
			var evals []Eval
			if err := json.Unmarshal(out["scale_teams_as_corrector"], &evals); err != nil {
				t.Fatalf("unmarshal curated: %v", err)
			}
			if evals[0].ProjectPath != tc.want {
				t.Errorf("ProjectPath = %q, want %q", evals[0].ProjectPath, tc.want)
			}
		})
	}
}

func TestPiscineGraded(t *testing.T) {
	t.Parallel()
	poolTrue := func(string) bool { return true }
	poolFalse := func(string) bool { return false } // owner past their own pool
	cases := []struct {
		name        string
		projectPath string
		inPool      func(string) bool
		want        bool
	}{
		// The path is authoritative in both directions and beats the window. Foreign piscine
		// (out of pool but a piscine path) is still piscine; a cursus path stays cursus even
		// when its date lands inside the pool window.
		{"piscine path, out of pool", "pedago_world/c-piscine/c-piscine-c-00", poolFalse, true},
		{"cursus path suppresses in-pool window", "pedago_world/42-cursus/inner-circle/minitalk", poolTrue, false},
		// Path absent -> the pool-month window classifies (owner's own piscine).
		{"no path, in pool window", "", poolTrue, true},
		{"no path, out of pool window", "", poolFalse, false},
		{"no path, nil classifier", "", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Eval{ProjectPath: tc.projectPath, BeginAt: "2024-07-15T10:00:00Z"}.PiscineGraded(tc.inPool)
			if got != tc.want {
				t.Errorf("PiscineGraded = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseMonth(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want time.Month
		ok   bool
	}{
		{"July", time.July, true},
		{"  august ", time.August, true},
		{"DECEMBER", time.December, true},
		{"", 0, false},
		{"smarch", 0, false},
		{"7", 0, false},
	}
	for _, c := range cases {
		got, ok := parseMonth(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseMonth(%q) = (%v, %v), want (%v, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestPiscineByPool(t *testing.T) {
	t.Parallel()
	iso := func(y int, m time.Month, d int) string {
		return time.Date(y, m, d, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	}
	cases := []struct {
		name            string
		poolMonth, year string
		beginAt         string
		wantNil         bool // classifier itself is nil (unusable pool metadata)
		want            bool
	}{
		{"month before pool", "July", "2024", iso(2024, time.June, 15), false, true},
		{"during pool month", "July", "2024", iso(2024, time.July, 3), false, true},
		{"month after pool", "July", "2024", iso(2024, time.August, 20), false, true},
		{"two months after is out", "July", "2024", iso(2024, time.September, 1), false, false},
		{"two months before is out", "July", "2024", iso(2024, time.May, 31), false, false},
		{"december pool wraps to january", "December", "2024", iso(2025, time.January, 10), false, true},
		{"january pool wraps to december", "January", "2025", iso(2024, time.December, 20), false, true},
		{"empty pool -> nil classifier", "", "", iso(2024, time.July, 3), true, false},
		{"bad year -> nil classifier", "July", "notayear", iso(2024, time.July, 3), true, false},
		{"unparseable beginAt is not piscine", "July", "2024", "x", false, false},
	}
	for _, c := range cases {
		classify := PiscineByPool(c.poolMonth, c.year)
		if c.wantNil {
			if classify != nil {
				t.Errorf("%s: classifier should be nil", c.name)
			}
			continue
		}
		if classify == nil {
			t.Fatalf("%s: classifier unexpectedly nil", c.name)
		}
		if got := classify(c.beginAt); got != c.want {
			t.Errorf("%s: classify(%q) = %v, want %v", c.name, c.beginAt, got, c.want)
		}
	}
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestCurateProfileAndTitles(t *testing.T) {
	t.Parallel()
	rawMe := `{
		"login": "owner", "displayname": "Test User", "email": "t@e.st",
		"wallet": 100, "correction_point": 7,
		"cursus_users": [{"level": 9.5, "begin_at": "2024-10-01T00:00:00Z", "cursus": {"name": "42cursus"}, "skills": [{"name": "Rigor", "level": 4.2}]}],
		"achievements": [{"name": "First blood", "tier": "easy", "description": "d"}],
		"titles": [{"id": 1, "name": "the Beloved"}],
		"titles_users": [{"title_id": 1, "selected": true}]
	}`
	out := Curate(map[string]json.RawMessage{"me": json.RawMessage(rawMe)})

	var p Profile
	if err := json.Unmarshal(out["me"], &p); err != nil {
		t.Fatalf("unmarshal me: %v", err)
	}
	if p.Name != "Test User" || p.Email != "t@e.st" || p.CorrectionPoint != 7 {
		t.Errorf("profile fields: %+v", p)
	}
	if len(p.Cursus) != 1 || p.Cursus[0].Level != 9.5 || p.Cursus[0].BeginAt != "2024-10-01T00:00:00Z" || len(p.Cursus[0].Skills) != 1 {
		t.Errorf("cursus/skills: %+v", p.Cursus)
	}
	if len(p.Achievements) != 1 || p.Achievements[0].Name != "First blood" {
		t.Errorf("achievements: %+v", p.Achievements)
	}

	var titles []Title
	if err := json.Unmarshal(out["titles_users"], &titles); err != nil {
		t.Fatalf("unmarshal titles: %v", err)
	}
	if len(titles) != 1 || titles[0].Name != "the Beloved" || !titles[0].Selected {
		t.Errorf("titles: %+v", titles)
	}
}

func TestCurateIsPresenceDriven(t *testing.T) {
	t.Parallel()
	// Only-locations input (a partial re-sync) must not synthesise a me/titles_users
	// key, or the store's merge would clobber the existing profile.
	out := Curate(map[string]json.RawMessage{"locations": json.RawMessage(`[{"host":"c1","begin_at":"x"}]`)})
	if _, ok := out["me"]; ok {
		t.Error("absent me should not be emitted")
	}
	if _, ok := out["titles_users"]; ok {
		t.Error("absent me should not synthesise titles_users")
	}
	if _, ok := out["locations"]; !ok {
		t.Error("present locations should be curated")
	}

	// Redundant resources are dropped entirely.
	out = Curate(map[string]json.RawMessage{
		"me":           json.RawMessage(`{"login":"x"}`),
		"scale_teams":  json.RawMessage(`[{"id":1}]`),
		"cursus_users": json.RawMessage(`[{"id":1}]`),
	})
	if _, ok := out["scale_teams"]; ok {
		t.Error("plain scale_teams should be dropped")
	}
	if _, ok := out["cursus_users"]; ok {
		t.Error("standalone cursus_users should be dropped")
	}
}

func TestLoginScrubber(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		logins []string
		in     string
		want   string
	}{
		{"empty logins is identity", nil, "alice helped bob", "alice helped bob"},
		{"single login removed", []string{"alice"}, "alice helped", "[redacted] helped"},
		{"all occurrences removed", []string{"bob"}, "bob and bob", "[redacted] and [redacted]"},
		{"duplicate logins handled", []string{"bob", "bob"}, "bob", "[redacted]"},
		// Longest-first ordering: "jdoe2" must match before "jdoe" so it isn't left as "[redacted]2".
		{"prefix login does not partial-match longer", []string{"jdoe", "jdoe2"}, "jdoe2 and jdoe", "[redacted] and [redacted]"},
		// Documented over-removal: a login that is a substring of another word scrubs it too.
		{"substring over-removal (documented)", []string{"al"}, "also alpha", "[redacted]so [redacted]pha"},
		// Documented limitation: matching is case-sensitive, so a differing case is missed.
		{"case-sensitive miss (documented)", []string{"alice"}, "Alice helped", "Alice helped"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := loginScrubber(c.logins)(c.in); got != c.want {
				t.Errorf("loginScrubber(%v)(%q) = %q, want %q", c.logins, c.in, got, c.want)
			}
		})
	}
}
