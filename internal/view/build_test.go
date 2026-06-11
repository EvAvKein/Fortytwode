package view

import (
	"encoding/json"
	"testing"

	"github.com/EvAvKein/Fortytwode/internal/view/model"
)

func TestSectionPublic(t *testing.T) {
	// locations is private by default; others public by default; overrides win.
	if SectionPublic(nil, "locations") {
		t.Error("locations should be private by default")
	}
	if !SectionPublic(nil, "projects_users") {
		t.Error("projects should be public by default")
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
		"me":             json.RawMessage(`{"login":"tester","email":"t@e.st"}`),
		"projects_users": json.RawMessage(`[{"name":"libft","status":"finished","validated":true}]`),
		"locations":      json.RawMessage(`[{"begin_at":"2026-01-01T00:00:00Z","host":"c1"}]`),
	}

	owner := Build(snaps, true, nil)
	if !hasSection(owner, "Locations") || !hasSection(owner, "Projects") {
		t.Error("owner should see both Projects and Locations")
	}
	if !hasKV(owner.Profile, "Email") {
		t.Error("owner should see the Email row")
	}
	if !section(owner, "Locations").Private {
		t.Error("owner's private-by-default Locations should be badged Private")
	}
	if section(owner, "Projects").Private {
		t.Error("owner's public Projects should not be badged Private")
	}

	pub := Build(snaps, false, nil)
	if hasSection(pub, "Locations") {
		t.Error("non-owner should not see Locations by default")
	}
	if !hasSection(pub, "Projects") {
		t.Error("non-owner should see Projects")
	}
	if hasKV(pub.Profile, "Email") {
		t.Error("non-owner should not see the Email row")
	}

	opted := Build(snaps, false, map[string]bool{"locations": true})
	if !hasSection(opted, "Locations") {
		t.Error("non-owner should see Locations when the owner opts it public")
	}
}

func hasSection(d model.PageData, title string) bool {
	for _, s := range d.Sections {
		if s.Title == title {
			return true
		}
	}
	return false
}

// section returns the named section, or a zero Section if absent.
func section(d model.PageData, title string) model.Section {
	for _, s := range d.Sections {
		if s.Title == title {
			return s
		}
	}
	return model.Section{}
}

func hasKV(p *model.Profile, key string) bool {
	if p == nil {
		return false
	}
	for _, kv := range p.Rows {
		if kv.Key == key {
			return true
		}
	}
	return false
}
