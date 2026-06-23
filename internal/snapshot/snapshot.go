// Package snapshot reduces a raw 42 API snapshot to the minimal, curated form the
// app persists and renders. Curation is where data minimisation happens: only the
// fields the dashboard actually shows are kept, and every identity that isn't the
// account owner's (correctors, corrected students, teammates, feedback authors,
// truants) is stripped — so the database never holds third-party personal data.
// The raw snapshot is still available to the owner at sync time (download/CLI);
// only the persisted copy is curated.
package snapshot

import (
	"cmp"
	"encoding/json"
	"sort"
	"strings"

	"github.com/EvAvKein/Fortytwode/internal/api42"
)

// ----------------------------------------------------------------------------
// Curated types — only the fields internal/view renders, plus the truancy fact
// and achievements. JSON tags are the persisted snapshot's shape.
// ----------------------------------------------------------------------------

// Profile is the curated /me: the owner's own identity, header stats, cursus, and
// achievements. No embedded user objects, groups, languages, or nested
// projects/scale_teams.
type Profile struct {
	Login           string        `json:"login"`
	Name            string        `json:"name"` // pre-resolved displayname||usual_full_name||login
	Email           string        `json:"email,omitempty"`
	ImageURL        string        `json:"image_url,omitempty"`
	Campus          string        `json:"campus,omitempty"`
	Wallet          int           `json:"wallet"`
	CorrectionPoint int           `json:"correction_point"`
	PoolMonth       string        `json:"pool_month,omitempty"`
	PoolYear        string        `json:"pool_year,omitempty"`
	Cursus          []Cursus      `json:"cursus,omitempty"`
	Achievements    []Achievement `json:"achievements,omitempty"`
}

type Cursus struct {
	Name   string  `json:"name"`
	Level  float64 `json:"level"`
	Grade  string  `json:"grade,omitempty"`
	Skills []Skill `json:"skills,omitempty"`
}

type Skill struct {
	Name  string  `json:"name"`
	Level float64 `json:"level"`
}

type Achievement struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Tier        string `json:"tier,omitempty"`
}

// Title is one earned title, name pre-joined from me.Titles. Stored under the
// "titles_users" key (derived from /me, whose standalone endpoint is role-gated).
type Title struct {
	Name     string `json:"name"`
	Selected bool   `json:"selected"`
}

type Coalition struct {
	Name  string `json:"name"`
	Score int    `json:"score"`
	Color string `json:"color,omitempty"`
}

type Project struct {
	Name      string `json:"name"`
	FinalMark *int   `json:"final_mark"`
	Status    string `json:"status"`
	Validated *bool  `json:"validated"`
	When      string `json:"when,omitempty"` // marked_at||updated_at, pre-resolved
}

// Eval is one peer evaluation, stripped of every participant's login. It keeps the
// project name (resolved from the team's project_id), the outcome (mark/flag), the
// corrector's write-up (Comment), the peer rating left on it (Rating/FeedbackComment,
// author dropped), and whether a truancy occurred. Team holds the team's name only
// when it's distinctive (see genericTeamName).
type Eval struct {
	Project         string `json:"project,omitempty"`
	Team            string `json:"team,omitempty"`
	FinalMark       *int   `json:"final_mark"`
	FlagName        string `json:"flag"`
	FlagPositive    bool   `json:"flag_positive"`
	BeginAt         string `json:"begin_at"`
	Comment         string `json:"comment,omitempty"`
	Rating          *int   `json:"rating,omitempty"`
	FeedbackComment string `json:"feedback_comment,omitempty"`
	Truant          bool   `json:"truant"`
}

type Location struct {
	Host    string  `json:"host"`
	BeginAt string  `json:"begin_at"`
	EndAt   *string `json:"end_at"`
}

type Event struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	BeginAt string `json:"begin_at"`
}

type Quest struct {
	Name        string   `json:"name"`
	ValidatedAt *string  `json:"validated_at"`
	Prct        *float64 `json:"prct"`
}

type CorrectionPoint struct {
	Reason    string `json:"reason"`
	Sum       int    `json:"sum"`
	Total     int    `json:"total"`
	CreatedAt string `json:"created_at"`
}

// ----------------------------------------------------------------------------
// Curate
// ----------------------------------------------------------------------------

// Curate maps a raw snapshot to its curated form, keeping the same resource keys.
// It is presence-driven: only keys present in raw are emitted, so it composes with
// the store's partial-merge update (a resource absent from a re-sync keeps its
// prior persisted value). Unknown/redundant keys (scale_teams plain, cursus_users
// standalone) are dropped — the embedded /me copy and the as_corrector/as_corrected
// splits already cover them.
func Curate(raw map[string]json.RawMessage) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}

	// /me is special: it yields both the curated profile and the titles list. The
	// owner's own login is kept; everyone else's is scrubbed from evaluations.
	ownerLogin := ""
	if rawMe, ok := raw["me"]; ok {
		var me api42.Me
		if err := json.Unmarshal(rawMe, &me); err == nil {
			ownerLogin = me.Login
			out["me"] = marshal(profileFrom(me))
			out["titles_users"] = marshal(titlesFrom(me))
		}
	}

	curateInto(out, raw, "coalitions", coalitionFrom)
	curateInto(out, raw, "projects_users", projectFrom)
	// Evals carry only a project_id; resolve it to a name via projects_users (the
	// scale_team payload has no project name of its own). Absent on a partial
	// re-sync -> names simply don't resolve, which the view falls back from.
	projectNames := map[int]string{}
	if rawPU, ok := raw["projects_users"]; ok {
		var pus []api42.ProjectUser
		if json.Unmarshal(rawPU, &pus) == nil {
			for _, pu := range pus {
				projectNames[pu.Project.ID] = pu.Project.Name
			}
		}
	}
	evalMapper := func(st api42.ScaleTeam) Eval { return evalFrom(st, ownerLogin, projectNames) }
	curateInto(out, raw, "scale_teams_as_corrector", evalMapper)
	curateInto(out, raw, "scale_teams_as_corrected", evalMapper)
	curateInto(out, raw, "locations", locationFrom)
	curateInto(out, raw, "events_users", eventFrom)
	curateInto(out, raw, "quests_users", questFrom)
	curateInto(out, raw, "correction_point_historics", correctionPointFrom)

	return out
}

// curateInto unmarshals raw[key] into []A, maps each element through fn, and stores
// the marshalled []C under the same key. A missing or malformed key is skipped.
func curateInto[A, C any](out, raw map[string]json.RawMessage, key string, fn func(A) C) {
	rawVal, ok := raw[key]
	if !ok {
		return
	}
	var in []A
	if err := json.Unmarshal(rawVal, &in); err != nil {
		return
	}
	curated := make([]C, len(in))
	for i, v := range in {
		curated[i] = fn(v)
	}
	out[key] = marshal(curated)
}

func marshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ----------------------------------------------------------------------------
// Per-resource mappers (api42.* -> curated)
// ----------------------------------------------------------------------------

func profileFrom(me api42.Me) Profile {
	p := Profile{
		Login:           me.Login,
		Name:            cmp.Or(me.Displayname, me.UsualFullName, me.Login),
		Email:           me.Email,
		ImageURL:        deref(me.Image.Link),
		Wallet:          me.Wallet,
		CorrectionPoint: me.CorrectionPoint,
		PoolMonth:       deref(me.PoolMonth),
		PoolYear:        deref(me.PoolYear),
	}
	if len(me.Campus) > 0 {
		p.Campus = me.Campus[0].Name
	}
	for _, cu := range me.CursusUsers {
		c := Cursus{
			Name:  cu.Cursus.Name,
			Level: cu.Level,
			Grade: deref(cu.Grade),
		}
		for _, s := range cu.Skills {
			c.Skills = append(c.Skills, Skill{Name: s.Name, Level: s.Level})
		}
		p.Cursus = append(p.Cursus, c)
	}
	for _, a := range me.Achievements {
		p.Achievements = append(p.Achievements, Achievement{Name: a.Name, Description: a.Description, Tier: a.Tier})
	}
	return p
}

func titlesFrom(me api42.Me) []Title {
	names := map[int]string{}
	for _, t := range me.Titles {
		names[t.ID] = t.Name
	}
	out := make([]Title, 0, len(me.TitlesUsers))
	for _, tu := range me.TitlesUsers {
		out = append(out, Title{Name: names[tu.TitleID], Selected: tu.Selected})
	}
	return out
}

func coalitionFrom(c api42.Coalition) Coalition {
	return Coalition{Name: c.Name, Score: c.Score, Color: c.Color}
}

func projectFrom(p api42.ProjectUser) Project {
	return Project{
		Name:      p.Project.Name,
		FinalMark: p.FinalMark,
		Status:    p.Status,
		Validated: p.Validated,
		When:      cmp.Or(deref(p.MarkedAt), p.UpdatedAt),
	}
}

func evalFrom(st api42.ScaleTeam, ownerLogin string, projectNames map[int]string) Eval {
	// Collect every participant's login except the owner's, to scrub from the kept
	// name/text fields: a solo project's team name is literally "<login>'s group",
	// and free-text comments can name people too.
	var others []string
	add := func(l string) {
		if l != "" && l != ownerLogin {
			others = append(others, l)
		}
	}
	add(st.Corrector.Login)
	add(st.Truant.Login)
	for _, c := range st.Correcteds {
		add(c.Login)
	}
	for _, fb := range st.Feedbacks {
		add(fb.User.Login)
	}
	for _, u := range st.Team.Users {
		add(u.Login)
	}
	scrub := loginScrubber(others)

	// Keep the team name only when it's distinctive (see genericTeamName).
	team := scrub(st.Team.Name)
	if genericTeamName(team) {
		team = ""
	}

	e := Eval{
		Project:      projectNames[st.Team.ProjectID],
		Team:         team,
		FinalMark:    st.FinalMark,
		FlagName:     st.Flag.Name,
		FlagPositive: st.Flag.Positive,
		BeginAt:      st.BeginAt,
		Comment:      scrub(deref(st.Comment)),
		Truant:       st.Truant.ID != 0, // empty {} (id 0) means nobody was truant
	}
	if len(st.Feedbacks) > 0 {
		fb := st.Feedbacks[0] // author (fb.User) deliberately dropped
		e.Rating = &fb.Rating
		e.FeedbackComment = scrub(fb.Comment)
	}
	return e
}

// genericTeamName reports whether a team name is one of 42's auto-generated defaults
// ("<login>'s group", with a "-N" suffix on retries) or otherwise just names a
// participant ("<login>'s team", any capitalisation). Such names add nothing over the
// project name and tend to embed an identity that login-scrubbing alone can miss (e.g.
// a teammate's first name in "Viet and Cristi's Team"), so they're dropped.
func genericTeamName(name string) bool {
	l := strings.ToLower(name)
	return strings.Contains(l, "'s group") || strings.Contains(l, "'s team")
}

// loginScrubber returns a function that removes the given logins from a string,
// each replaced with "[redacted]". Logins are matched longest-first so one that is a
// prefix of another can't partially match. It errs toward over-removal (a login
// that happens to be a common substring may scrub more than intended) — acceptable,
// since the goal is to keep third-party identities out of persisted free text.
func loginScrubber(logins []string) func(string) string {
	if len(logins) == 0 {
		return func(s string) string { return s }
	}
	seen := map[string]bool{}
	uniq := make([]string, 0, len(logins))
	for _, l := range logins {
		if !seen[l] {
			seen[l] = true
			uniq = append(uniq, l)
		}
	}
	sort.Slice(uniq, func(i, j int) bool { return len(uniq[i]) > len(uniq[j]) })
	return func(s string) string {
		for _, l := range uniq {
			s = strings.ReplaceAll(s, l, "[redacted]")
		}
		return s
	}
}

func locationFrom(l api42.Location) Location {
	return Location{Host: l.Host, BeginAt: l.BeginAt, EndAt: l.EndAt}
}

func eventFrom(e api42.EventUser) Event {
	return Event{Name: e.Event.Name, Kind: e.Event.Kind, BeginAt: e.Event.BeginAt}
}

func questFrom(q api42.QuestUser) Quest {
	return Quest{Name: q.Quest.Name, ValidatedAt: q.ValidatedAt, Prct: q.Prct}
}

func correctionPointFrom(h api42.CorrectionPointHistoric) CorrectionPoint {
	return CorrectionPoint{Reason: h.Reason, Sum: h.Sum, Total: h.Total, CreatedAt: h.CreatedAt}
}
