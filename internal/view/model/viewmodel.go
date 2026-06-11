// Package model holds the view-model types rendered by the view package and its
// components: the curated shapes the snapshot is built into, kept separate so
// both the page templates and the reusable components can reference them without
// an import cycle.
package model

// Cell is one table cell. Tone is a CSS class ("", "good", "bad", "muted").
type Cell struct {
	Text string
	Tone string
}

// Section is a curated, collapsible panel of one resource. Most sections are
// tables (Columns + Rows); evaluations instead fill Evals and render as cards.
type Section struct {
	Title   string
	Count   int    // true total, shown in the header pill
	Summary string // one-line stat, e.g. "42 passed · 9 failed"
	Private bool   // owner-only badge: hidden from other viewers
	Columns []string
	Rows    [][]Cell
	Evals   []EvalItem // when set, the panel renders an EvalList instead of a Table
}

// EvalItem is one peer evaluation rendered as a card (not a table row): project +
// date frame the full feedback, with mark, flag and rating shown below.
type EvalItem struct {
	Project  string // project name, or "—"
	Team     string // distinctive team name shown beside the project; "" when generic
	Date     string // ymd(BeginAt)
	Mark     Cell   // dashInt(FinalMark) + markTone
	Flag     Cell   // flag name (+ "· truant") + tone
	Rating   string // stars() for "given" evals, "" otherwise
	Feedback string // full text; "" -> muted placeholder in the template
}

// KV is one labelled value in the profile header.
type KV struct{ Key, Value string }

// SkillBar is a single skill with a proportional bar (Style = "width:NN%").
type SkillBar struct {
	Name, Level, Style string
}

// CoalitionBadge is the coalition pill in the header (Style = "background:#hex").
type CoalitionBadge struct {
	Name, Score, Style string
}

// CursusSkills is the deep "Cursus & Skills" panel.
type CursusSkills struct {
	Cursus Section
	Skills []SkillBar
}

// Profile is the rendered header view of the "me" snapshot.
type Profile struct {
	Name, Login, ImageURL string
	Rows                  []KV
	Coalition             *CoalitionBadge
}

// PageData is everything a single render of the dashboard needs.
type PageData struct {
	Status       string
	IsError      bool
	Profile      *Profile
	CursusSkills *CursusSkills
	Sections     []Section
	Owner        bool   // viewer owns this profile -> show the owner nav
	Login        string // 42 login of the profile being viewed (for links)
}

// SettingsToggle is one section's public/private switch on the settings page.
type SettingsToggle struct {
	Key, Label string
	Public     bool
	HasData    bool
}

// SettingsData is the settings page view: the public opt-in plus per-section toggles.
type SettingsData struct {
	IsPublic bool
	Toggles  []SettingsToggle
	Login    string
	Saved    bool
}
