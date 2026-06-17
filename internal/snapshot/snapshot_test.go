package snapshot

import (
	"encoding/json"
	"strings"
	"testing"
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
	if e.Rating == nil || *e.Rating != 5 || e.FeedbackComment != "Very thorough." {
		t.Errorf("dropped feedback rating/comment: %+v", e)
	}
	if !e.Truant {
		t.Error("truancy fact (no-show id 99) should be recorded")
	}
}

// TestCurateKeepsDistinctiveTeamName checks the keep/drop split: a real team name
// survives, while default-style "<x>'s group"/"<x>'s team" names (which embed
// identities) are dropped regardless of capitalisation.
func TestCurateKeepsDistinctiveTeamName(t *testing.T) {
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

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestCurateProfileAndTitles(t *testing.T) {
	rawMe := `{
		"login": "owner", "displayname": "Test User", "email": "t@e.st",
		"wallet": 100, "correction_point": 7,
		"cursus_users": [{"level": 9.5, "cursus": {"name": "42cursus"}, "skills": [{"name": "Rigor", "level": 4.2}]}],
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
	if len(p.Cursus) != 1 || p.Cursus[0].Level != 9.5 || len(p.Cursus[0].Skills) != 1 {
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
