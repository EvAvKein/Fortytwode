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

// PanelHeader holds the common heading fields for every collapsible panel.
type PanelHeader struct {
	Title   string
	Count   int    // true total, shown in the header pill
	Summary string // one-line stat, e.g. "42 passed · 9 failed"
	Private bool   // owner-only badge: hidden from other viewers
}

// TableSection is a curated, collapsible panel rendered as a table.
type TableSection struct {
	PanelHeader
	Columns []string
	Rows    [][]Cell
}

// EvalSection is a curated, collapsible panel rendered as a list of eval cards.
type EvalSection struct {
	PanelHeader
	Evals []EvalItem
}

// SkillsSection is the collapsed Skills panel with the radar chart.
type SkillsSection struct {
	PanelHeader
	Skills []SkillBar
}

// ContactSection is the simple email contact panel.
type ContactSection struct {
	PanelHeader
	Email string
}

// Sections holds every collapsible profile panel as a named field so the page
// template can render each one explicitly with the right component.
type Sections struct {
	Contact          *ContactSection
	Projects         *TableSection
	EvalsReceived    *EvalSection
	EvalsGiven       *EvalSection
	CorrectionPoints *TableSection
	Quests           *TableSection
	Titles           *TableSection
	Achievements     *TableSection
	Events           *TableSection
	Locations        *TableSection
	Skills           *SkillsSection
}

// CursusRow is one cursus shown compactly in the profile header.
type CursusRow struct {
	Name, Grade, Level string
	Latest             bool
}

// PointsCard groups eval points and wallet in the header; it can be private.
type PointsCard struct {
	Eval, Wallet int
	Private      bool
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

// SkillBar is a single skill with a proportional bar (Pct = NN).
type SkillBar struct {
	Name, Level string
	Pct         int
	Index       int // 1-based number shown on the radar dot and in the legend
}

// CoalitionBadge is the coalition indicator in the header (Color = "#hex").
type CoalitionBadge struct {
	Name, Score, Color string
	Private            bool // owner-only badge: hidden from other viewers
}

// Profile is the rendered header view of the "me" snapshot.
type Profile struct {
	Name, Login, ImageURL string
	PrimaryStats          []KV // Campus, Selection pool
	Points                *PointsCard
	Cursus                []CursusRow
	Coalition             *CoalitionBadge
}

// PageData is everything a single render of the dashboard needs.
type PageData struct {
	Status  string
	IsError bool
	Profile *Profile
	Sections
	Owner      bool   // viewer owns this profile -> show the owner nav
	Login      string // 42 login of the profile being viewed (for links)
	LastSynced string // formatted "Synced: ..." timestamp for owners; empty otherwise
	CanResync  bool   // true when the owner's cooldown has expired
}

// SettingsToggle is one section's public/private switch on the settings page.
type SettingsToggle struct {
	Key, Label string
	Public     bool
	Default    bool
	HasData    bool
}

// SettingsData is the settings page view: the public opt-in plus per-section toggles,
// plus account forms (email/password) and their inline feedback.
type SettingsData struct {
	IsPublic      bool
	Toggles       []SettingsToggle
	Login         string
	Saved         bool
	LastSynced    string // formatted "Synced: ..." timestamp
	CanResync     bool   // true when the owner's cooldown has expired
	Email         string
	EmailError       string
	EmailSaved       bool
	PasswordError    string
	PasswordSaved    bool
	DeletionRequested bool
}
