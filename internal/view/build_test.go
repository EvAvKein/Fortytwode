package view

import (
	"encoding/json"
	"testing"
)

func TestSectionPublic(t *testing.T) {
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

	private := Build(snaps, false, map[string]bool{"coalitions": false, "achievements": false})
	if private.Profile != nil && private.Profile.Coalition != nil {
		t.Error("non-owner should not see the Coalition card when opted private")
	}
	if private.Sections.Achievements != nil {
		t.Error("non-owner should not see Achievements when opted private")
	}

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
}
