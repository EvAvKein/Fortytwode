package view

import (
	"encoding/json"
	"github.com/EvAvKein/Fortytwode/internal/view/model"
	"testing"
)

func TestSectionPublic(t *testing.T) {
	t.Parallel()
	// coalitions, locations, skills, contact, and points are private by default; others public by default; overrides win.
	for _, key := range []string{"locations", "skills", "contact", "points"} {
		if SectionPublic(nil, key) {
			t.Errorf("%s should be private by default", key)
		}
	}
	if !SectionPublic(nil, "projects_users") {
		t.Error("projects should be public by default")
	}
	if SectionPublic(nil, "coalitions") {
		t.Error("coalitions should be private by default")
	}
	if !SectionPublic(nil, "achievements") {
		t.Error("achievements should be public by default")
	}
	if !SectionPublic(map[string]bool{"locations": true}, "locations") {
		t.Error("override should make locations public")
	}
	if SectionPublic(map[string]bool{"projects_users": false}, "projects_users") {
		t.Error("override should make projects private")
	}
}

func TestBuildVisibility(t *testing.T) {
	t.Parallel()
	snaps := map[string]json.RawMessage{
		"me": json.RawMessage(`{
			"login":"tester",
			"email":"t@e.st",
			"wallet": 100,
			"correction_point": 5,
			"cursus": [
				{"name":"42cursus","level":9.5,"grade":"Member","skills":[{"name":"Rigor","level":4.2}]}
			],
			"achievements": [{"name":"First blood","tier":"easy","description":"d"}]
		}`),
		"projects_users": json.RawMessage(`[{"name":"libft","status":"finished","validated":true}]`),
		"locations":      json.RawMessage(`[{"begin_at":"2026-01-01T00:00:00Z","host":"c1"}]`),
		"coalitions":     json.RawMessage(`[{"name":"The Guards","score":183556,"color":"#c77aa5"}]`),
	}

	t.Run("OwnerSeesAll", func(t *testing.T) {
		owner := Build(snaps, true, nil)
		if owner.Sections.Locations == nil || owner.Sections.Projects == nil {
			t.Error("owner should see Locations and Projects")
		}
		if owner.Sections.Contact == nil {
			t.Error("owner should see Contact")
		}
		if !owner.Sections.Locations.Private {
			t.Error("owner's private-by-default Locations should be badged Private")
		}
		if owner.Sections.Projects.Private {
			t.Error("owner's public Projects should not be badged Private")
		}
		if owner.Profile == nil || owner.Profile.Points == nil {
			t.Fatal("owner should see the Points Remaining card")
		}
		if !owner.Profile.Points.Private {
			t.Error("owner's private-by-default Points card should be badged Private")
		}
		if owner.Profile.Coalition == nil {
			t.Fatal("owner should see the Coalition card")
		}
		if !owner.Profile.Coalition.Private {
			t.Error("owner's private-by-default Coalition card should be badged Private")
		}
		if owner.Sections.Skills == nil {
			t.Fatal("owner should see Skills")
		}
		if !owner.Sections.Skills.Private {
			t.Error("owner's private-by-default Skills should be badged Private")
		}
		if owner.Sections.Achievements == nil {
			t.Fatal("owner should see Achievements")
		}
		if owner.Sections.Achievements.Private {
			t.Error("owner's public-by-default Achievements should not be badged Private")
		}
	})

	t.Run("PublicSeesDefaults", func(t *testing.T) {
		pub := Build(snaps, false, nil)
		if pub.Sections.Locations != nil {
			t.Error("non-owner should not see Locations by default")
		}
		if pub.Sections.Contact != nil {
			t.Error("non-owner should not see Contact by default")
		}
		if pub.Sections.Projects == nil {
			t.Error("non-owner should see Projects")
		}
		if pub.Profile != nil && pub.Profile.Points != nil {
			t.Error("non-owner should not see the Points Remaining card by default")
		}
		if pub.Profile != nil && pub.Profile.Coalition != nil {
			t.Error("non-owner should not see the Coalition card by default")
		}
		if pub.Sections.Skills != nil {
			t.Error("non-owner should not see Skills by default")
		}
		if pub.Sections.Achievements == nil {
			t.Error("non-owner should see Achievements by default")
		}
	})

	t.Run("PrivateOverrides", func(t *testing.T) {
		private := Build(snaps, false, map[string]bool{"coalitions": false, "achievements": false})
		if private.Profile != nil && private.Profile.Coalition != nil {
			t.Error("non-owner should not see the Coalition card when opted private")
		}
		if private.Sections.Achievements != nil {
			t.Error("non-owner should not see Achievements when opted private")
		}
	})

	t.Run("OptedInOverrides", func(t *testing.T) {
		opted := Build(snaps, false, map[string]bool{"coalitions": true, "locations": true, "contact": true, "points": true, "skills": true})
		if opted.Sections.Locations == nil {
			t.Error("non-owner should see Locations when opted public")
		}
		if opted.Sections.Contact == nil {
			t.Error("non-owner should see Contact when opted public")
		}
		if opted.Profile == nil || opted.Profile.Points == nil {
			t.Error("non-owner should see the Points Remaining card when opted public")
		}
		if opted.Sections.Skills == nil {
			t.Error("non-owner should see Skills when opted public")
		}
		if opted.Profile == nil || opted.Profile.Coalition == nil {
			t.Error("non-owner should see the Coalition card when opted public")
		}
	})
}

// Eval marks are toned red below the project's pass bar (50 piscine / 80 cursus). The
// project's gitlab path is the authoritative classifier and wins over the pool-month
// fallback; the fallback only classifies evals whose path is absent.
func TestBuildEvalMarkTone(t *testing.T) {
	t.Parallel()
	snaps := map[string]json.RawMessage{
		"me": json.RawMessage(`{"login":"tester","pool_month":"July","pool_year":"2024"}`),
		// project names are the lookup key below; marks all positive-flagged so the mark
		// tone (not the flag) is what's under test.
		"scale_teams_as_corrector": json.RawMessage(`[
			{"project":"cursusFail","project_path":"pedago_world/42-cursus/inner-circle/minitalk","final_mark":78,"flag":"Ok","flag_positive":true,"begin_at":"2025-03-01T10:00:00Z"},
			{"project":"pathBeatsPool","project_path":"pedago_world/42-cursus/inner-circle/minitalk","final_mark":78,"flag":"Ok","flag_positive":true,"begin_at":"2024-07-10T10:00:00Z"},
			{"project":"piscinePass","project_path":"pedago_world/c-piscine/c-piscine-c-00","final_mark":55,"flag":"Ok","flag_positive":true,"begin_at":"2024-07-05T10:00:00Z"},
			{"project":"poolFallbackFail","final_mark":45,"flag":"Ok","flag_positive":true,"begin_at":"2024-07-05T10:00:00Z"}
		]`),
	}
	d := Build(snaps, true, nil)
	if d.Sections.EvalsGiven == nil {
		t.Fatal("expected EvalsGiven section")
	}
	tone := map[string]string{}
	for _, e := range d.Sections.EvalsGiven.Evals {
		tone[e.Project] = e.Evaluator.Mark.Tone
	}
	// 78 on a cursus-path project (bar 80) fails.
	if tone["cursusFail"] != "bad" {
		t.Errorf("cursus 78 should be red, got %q", tone["cursusFail"])
	}
	// 78 on a cursus path, dated inside the pool window: the path must win (bar 80 -> red),
	// not the fallback (which would call the in-pool date piscine -> bar 50 -> pass).
	if tone["pathBeatsPool"] != "bad" {
		t.Errorf("cursus-path 78 should be red despite in-pool date, got %q", tone["pathBeatsPool"])
	}
	// 55 on a piscine-path day (bar 50) passes.
	if tone["piscinePass"] != "" {
		t.Errorf("piscine 55 should be neutral, got %q", tone["piscinePass"])
	}
	// 45 with no path, dated in the pool window: the fallback classifies it piscine (bar 50) -> red.
	if tone["poolFallbackFail"] != "bad" {
		t.Errorf("pathless in-pool 45 should be red, got %q", tone["poolFallbackFail"])
	}
}

// Each eval card splits by role: the evaluator's write-up carries the verdict they
// issued (mark/flag), each evaluated-side response carries the rating it gave — and
// legacy snapshots (flat rating/feedback_comment) surface identically to new-shape
// feedbacks[] entries.
func TestBuildEvalFeedbackTexts(t *testing.T) {
	t.Parallel()
	eval := `[{"project":"minitalk","final_mark":100,"flag":"Ok","flag_positive":true,
		"begin_at":"2025-03-01T10:00:00Z","comment":"Write-up.","feedbacks":[
			{"author":"tester","rating":4,"comment":"Own response."},
			{"rating":2,"comment":"Teammate response."}
		]},
		{"project":"pipex","final_mark":90,"flag":"Ok","flag_positive":true,
		"begin_at":"2025-01-01T10:00:00Z","comment":"Write-up.","rating":3,"feedback_comment":"Legacy response."}]`
	legacyEval := `[{"project":"libft","final_mark":80,"flag":"Ok","flag_positive":true,
		"begin_at":"2024-11-01T10:00:00Z","comment":"Write-up.","rating":4,"feedback_comment":"Response."}]`
	snaps := map[string]json.RawMessage{
		"me":                       json.RawMessage(`{"login":"tester"}`),
		"scale_teams_as_corrector": json.RawMessage(eval),
		"scale_teams_as_corrected": json.RawMessage(legacyEval),
	}
	d := Build(snaps, true, nil)
	if d.Sections.EvalsGiven == nil || d.Sections.EvalsReceived == nil {
		t.Fatal("expected both eval sections")
	}

	given := d.Sections.EvalsGiven.Evals[0]
	if given.Evaluator.Text != "Write-up." || given.Evaluator.Mark.Text != "100" || given.Evaluator.Flag.Text != "Ok" {
		t.Errorf("given evaluator: got %+v", given.Evaluator)
	}
	wantGiven := []model.EvaluateeFeedback{
		{Author: "tester", Text: "Own response.", Rating: stars(4)},
		{Author: "", Text: "Teammate response.", Rating: stars(2)}, // recorded-authorless entry stays authorless
	}
	if len(given.Evaluatees) != 2 || given.Evaluatees[0] != wantGiven[0] || given.Evaluatees[1] != wantGiven[1] {
		t.Errorf("given evaluatees: got %+v, want %+v", given.Evaluatees, wantGiven)
	}

	// A legacy entry on a "given" eval was authored by the evaluated party, so it
	// must not be attributed to the owner.
	legacyGiven := d.Sections.EvalsGiven.Evals[1]
	wantLegacyGiven := []model.EvaluateeFeedback{{Author: "", Text: "Legacy response.", Rating: stars(3)}}
	if len(legacyGiven.Evaluatees) != 1 || legacyGiven.Evaluatees[0] != wantLegacyGiven[0] {
		t.Errorf("given (legacy) evaluatees: got %+v, want %+v", legacyGiven.Evaluatees, wantLegacyGiven)
	}

	received := d.Sections.EvalsReceived.Evals[0]
	if received.Evaluator.Text != "Write-up." || received.Evaluator.Mark.Text != "80" {
		t.Errorf("received (legacy) evaluator: got %+v", received.Evaluator)
	}
	// Legacy entries recorded no author; the build attributes them to the owner.
	wantReceived := []model.EvaluateeFeedback{{Author: "tester", Text: "Response.", Rating: stars(4)}}
	if len(received.Evaluatees) != 1 || received.Evaluatees[0] != wantReceived[0] {
		t.Errorf("received (legacy) evaluatees: got %+v, want %+v", received.Evaluatees, wantReceived)
	}
}

// The current cursus (Latest) is the most recently begun, not the highest-level: a
// student early in 42cursus still ranks it above a completed, higher-level C Piscine.
// Legacy snapshots synced without begin_at fall back to non-piscine-first, then level.
func TestBuildCursusLatestByRecency(t *testing.T) {
	t.Parallel()

	t.Run("recency beats level", func(t *testing.T) {
		// C Piscine finished at a higher level but began earlier; 42cursus is newer -> Latest.
		snaps := map[string]json.RawMessage{"me": json.RawMessage(`{"login":"trupham","cursus":[
			{"name":"C Piscine","level":11.2,"begin_at":"2024-07-01T00:00:00Z"},
			{"name":"42cursus","level":2.5,"begin_at":"2024-09-01T00:00:00Z"}
		]}`)}
		rows := Build(snaps, true, nil).Profile.Cursus
		if len(rows) != 2 || rows[0].Name != "42cursus" || !rows[0].Latest {
			t.Errorf("expected 42cursus latest by recency, got %+v", rows)
		}
		if rows[1].Latest {
			t.Error("only the leading cursus should be Latest")
		}
	})

	t.Run("no dates: non-piscine outranks higher-level piscine", func(t *testing.T) {
		// Legacy snapshot without begin_at: the piscine finished higher, but 42cursus is the
		// real current cursus and must lead regardless of level.
		snaps := map[string]json.RawMessage{"me": json.RawMessage(`{"login":"legacy","cursus":[
			{"name":"C Piscine","level":11.2},
			{"name":"42cursus","level":2.5}
		]}`)}
		rows := Build(snaps, true, nil).Profile.Cursus
		if len(rows) != 2 || rows[0].Name != "42cursus" || !rows[0].Latest {
			t.Errorf("expected non-piscine (42cursus) latest without dates, got %+v", rows)
		}
	})

	t.Run("no dates, same piscine-ness: higher level leads", func(t *testing.T) {
		// Neither is a piscine, so level is the final tiebreak.
		snaps := map[string]json.RawMessage{"me": json.RawMessage(`{"login":"legacy2","cursus":[
			{"name":"42cursus","level":2.5},
			{"name":"Old Cursus","level":9.0}
		]}`)}
		rows := Build(snaps, true, nil).Profile.Cursus
		if len(rows) != 2 || rows[0].Name != "Old Cursus" || !rows[0].Latest {
			t.Errorf("expected highest-level-first among non-piscines, got %+v", rows)
		}
	})
}

// An empty snapshot map has no "me" resource, so Build can't unmarshal the
// profile and must degrade to an error page rather than panicking.
func TestBuildEmptySnapshot(t *testing.T) {
	t.Parallel()
	d := Build(map[string]json.RawMessage{}, true, nil)
	if !d.IsError {
		t.Errorf("empty snapshot should yield an error page, got %+v", d)
	}
}

// A malformed "me" snapshot is reported as an error page, not a panic.
func TestBuildInvalidMe(t *testing.T) {
	t.Parallel()
	d := Build(map[string]json.RawMessage{"me": json.RawMessage(`{not json`)}, true, nil)
	if !d.IsError {
		t.Errorf("invalid \"me\" should yield an error page, got %+v", d)
	}
}
