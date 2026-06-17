// Package view turns a stored 42 snapshot into the curated view models the page
// templates render, and serves the page and component templates themselves. The
// snapshot is already data-minimised (see internal/snapshot): each resource is
// unmarshalled into the curated snapshot.* structs and laid out into table rows;
// section visibility is enforced per viewer.
package view

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/EvAvKein/Fortytwode/internal/snapshot"
	"github.com/EvAvKein/Fortytwode/internal/view/model"
)

// ----------------------------------------------------------------------------
// Section visibility
// ----------------------------------------------------------------------------

// ToggleableSections is the ordered set of resource panels an owner can mark
// public or private. The keys match the Build() add() calls and the snapshot
// resource names.
var ToggleableSections = []struct{ Key, Label string }{
	{"projects_users", "Projects"},
	{"scale_teams_as_corrector", "Evaluations given"},
	{"scale_teams_as_corrected", "Evaluations received"},
	{"locations", "Locations"},
	{"events_users", "Events"},
	{"quests_users", "Quests"},
	{"titles_users", "Titles"},
	{"correction_point_historics", "Correction points"},
}

// sectionPrivateByDefault lists sections hidden from non-owners unless the owner
// explicitly opts them public.
var sectionPrivateByDefault = map[string]bool{"locations": true}

// SectionPublic reports whether a section is visible to non-owners, honouring the
// account's explicit overrides over the built-in defaults.
func SectionPublic(vis map[string]bool, key string) bool {
	if v, ok := vis[key]; ok {
		return v
	}
	return !sectionPrivateByDefault[key]
}

// Build assembles the dashboard from a curated snapshot. When owner is false the
// Email row and any non-public section (per vis) are dropped.
func Build(snaps map[string]json.RawMessage, owner bool, vis map[string]bool) model.PageData {
	var me snapshot.Profile
	if err := json.Unmarshal(snaps["me"], &me); err != nil {
		return model.PageData{IsError: true, Status: fmt.Sprintf("stored \"me\" snapshot is not valid JSON: %v", err)}
	}

	d := model.PageData{
		Profile:      buildProfile(me, load[snapshot.Coalition](snaps, "coalitions"), owner),
		CursusSkills: buildCursusSkills(me),
	}

	// add includes a section when it has data and the viewer may see it. The
	// owner also sees private sections, badged so they know what others can't.
	add := func(key string, build func() (model.Section, bool)) {
		public := SectionPublic(vis, key)
		if sec, ok := build(); ok && (owner || public) {
			sec.Private = owner && !public
			d.Sections = append(d.Sections, sec)
		}
	}
	add("projects_users", func() (model.Section, bool) { return buildProjects(load[snapshot.Project](snaps, "projects_users")) })
	add("scale_teams_as_corrector", func() (model.Section, bool) {
		return buildEvals("Evaluations given", true, load[snapshot.Eval](snaps, "scale_teams_as_corrector"))
	})
	add("scale_teams_as_corrected", func() (model.Section, bool) {
		return buildEvals("Evaluations received", false, load[snapshot.Eval](snaps, "scale_teams_as_corrected"))
	})
	add("locations", func() (model.Section, bool) { return buildLocations(load[snapshot.Location](snaps, "locations")) })
	add("events_users", func() (model.Section, bool) { return buildEvents(load[snapshot.Event](snaps, "events_users")) })
	add("quests_users", func() (model.Section, bool) { return buildQuests(load[snapshot.Quest](snaps, "quests_users")) })
	add("titles_users", func() (model.Section, bool) { return buildTitles(load[snapshot.Title](snaps, "titles_users")) })
	add("correction_point_historics", func() (model.Section, bool) {
		return buildCorrectionPoints(load[snapshot.CorrectionPoint](snaps, "correction_point_historics"))
	})

	// Achievements are the owner's own badges (no third-party data), so they're
	// shown to every viewer of the profile, like the cursus panel — not toggled.
	if sec, ok := buildAchievements(me); ok {
		d.Sections = append(d.Sections, sec)
	}

	return d
}

// load unmarshals one resource of the snapshot into a slice of T. A missing
// resource yields nil.
func load[T any](snaps map[string]json.RawMessage, name string) []T {
	raw := snaps[name]
	if raw == nil {
		return nil
	}
	var out []T
	if err := json.Unmarshal(raw, &out); err != nil {
		fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", name, err)
	}
	return out
}

// ----------------------------------------------------------------------------
// Header + deep sections
// ----------------------------------------------------------------------------

func buildProfile(p snapshot.Profile, coalitions []snapshot.Coalition, owner bool) *model.Profile {
	prof := &model.Profile{
		Name:     cmp.Or(p.Name, "Unknown"),
		Login:    p.Login,
		ImageURL: p.ImageURL,
	}

	add := func(key, value string) {
		if value != "" {
			prof.Rows = append(prof.Rows, model.KV{Key: key, Value: value})
		}
	}
	if owner {
		add("Email", p.Email) // email is owner-only
	}
	add("Campus", p.Campus)
	add("Wallet", strconv.Itoa(p.Wallet))
	add("Eval points", strconv.Itoa(p.CorrectionPoint))
	add("Pool", strings.TrimSpace(p.PoolMonth+" "+p.PoolYear))

	if len(coalitions) > 0 {
		c := coalitions[0]
		prof.Coalition = &model.CoalitionBadge{
			Name:  c.Name,
			Score: strconv.Itoa(c.Score),
			Color: cmp.Or(c.Color, "#00babc"),
		}
	}
	return prof
}

func buildCursusSkills(p snapshot.Profile) *model.CursusSkills {
	if len(p.Cursus) == 0 {
		return nil
	}
	sec := model.Section{
		Title:   "Cursus",
		Count:   len(p.Cursus),
		Columns: []string{"Cursus", "Level", "Grade", "Blackhole"},
	}
	for _, cu := range p.Cursus {
		sec.Rows = append(sec.Rows, []model.Cell{
			{Text: cu.Name},
			{Text: fmt.Sprintf("%.2f", cu.Level)},
			{Text: orDash(cu.Grade)},
			{Text: orDash(ymd(cu.BlackholedAt)), Tone: toneIf(cu.BlackholedAt != "", "bad")},
		})
	}
	return &model.CursusSkills{Cursus: sec, Skills: topSkills(p.Cursus, 12)}
}

// topSkills aggregates skills across cursus (taking each skill's highest level),
// returns the top n, and scales each bar relative to the strongest skill.
func topSkills(cursus []snapshot.Cursus, n int) []model.SkillBar {
	best := map[string]float64{}
	for _, cu := range cursus {
		for _, s := range cu.Skills {
			best[s.Name] = max(best[s.Name], s.Level)
		}
	}

	type skill struct {
		name  string
		level float64
	}
	var all []skill
	top := 0.0
	for name, lvl := range best {
		all = append(all, skill{name, lvl})
		top = max(top, lvl)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].level > all[j].level })
	if len(all) > n {
		all = all[:n]
	}

	bars := make([]model.SkillBar, len(all))
	for i, s := range all {
		pct := 100
		if top > 0 {
			pct = int(s.level / top * 100)
		}
		bars[i] = model.SkillBar{
			Name:  s.name,
			Level: fmt.Sprintf("%.2f", s.level),
			Style: fmt.Sprintf("width:%d%%", pct),
		}
	}
	return bars
}

func buildProjects(ps []snapshot.Project) (model.Section, bool) {
	if len(ps) == 0 {
		return model.Section{}, false
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].When > ps[j].When })

	passed, failed := 0, 0
	sec := model.Section{Title: "Projects", Count: len(ps), Columns: []string{"Project", "Mark", "Result", "When"}}
	for _, p := range ps {
		result := model.Cell{Text: p.Status, Tone: "muted"}
		if p.Status == "finished" {
			if p.Validated != nil && *p.Validated {
				result, passed = model.Cell{Text: "✔ pass", Tone: "good"}, passed+1
			} else {
				result, failed = model.Cell{Text: "✗ fail", Tone: "bad"}, failed+1
			}
		}
		sec.Rows = append(sec.Rows, []model.Cell{
			{Text: p.Name},
			{Text: dashInt(p.FinalMark), Tone: markTone(p.FinalMark)},
			result,
			{Text: ymd(p.When), Tone: "muted"},
		})
	}
	sec.Summary = fmt.Sprintf("%d passed · %d failed", passed, failed)
	return sec, true
}

// buildEvals renders one side of peer evaluations as cards. Participant logins are
// never stored, so instead of naming the other party each card surfaces the feedback
// that concerns the owner: on "received" the corrector's write-up (Comment); on "given"
// the rating + comment students left on the owner's correction. Truancy is flagged.
func buildEvals(title string, given bool, evs []snapshot.Eval) (model.Section, bool) {
	if len(evs) == 0 {
		return model.Section{}, false
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].BeginAt > evs[j].BeginAt })

	sec := model.Section{Title: title, Count: len(evs)}
	for _, e := range evs {
		feedback, rating := e.Comment, ""
		if given {
			feedback = e.FeedbackComment
			if e.Rating != nil {
				rating = stars(*e.Rating)
			}
		}
		flag := model.Cell{Text: e.FlagName, Tone: toneIf(!e.FlagPositive, "bad")}
		if e.Truant {
			flag = model.Cell{Text: strings.TrimSpace(e.FlagName + " · truant"), Tone: "bad"}
		}
		// Project name leads; the distinctive team name (when kept) rides alongside.
		// If the project didn't resolve, the team name stands in as the title.
		project, team := e.Project, e.Team
		if project == "" {
			project, team = team, ""
		}
		sec.Evals = append(sec.Evals, model.EvalItem{
			Project:  orDash(project),
			Team:     team,
			Date:     ymd(e.BeginAt),
			Mark:     model.Cell{Text: dashInt(e.FinalMark), Tone: markTone(e.FinalMark)},
			Flag:     flag,
			Rating:   rating,
			Feedback: strings.TrimSpace(feedback),
		})
	}
	sec.Summary = fmt.Sprintf("%d evaluation(s)", len(evs))
	return sec, true
}

// ----------------------------------------------------------------------------
// Light sections
// ----------------------------------------------------------------------------

func buildLocations(locs []snapshot.Location) (model.Section, bool) {
	if len(locs) == 0 {
		return model.Section{}, false
	}
	sort.Slice(locs, func(i, j int) bool { return locs[i].BeginAt > locs[j].BeginAt })

	var total time.Duration
	for _, l := range locs {
		if d, ok := sessionDur(l); ok {
			total += d
		}
	}

	sec := model.Section{Title: "Locations", Count: len(locs), Columns: []string{"Host", "Begin", "End", "Duration"}}
	for _, l := range locs {
		end, dur := "active", "—"
		if l.EndAt != nil {
			end = ymdhm(*l.EndAt)
			if d, ok := sessionDur(l); ok {
				dur = hoursMinutes(d)
			}
		}
		sec.Rows = append(sec.Rows, []model.Cell{
			{Text: l.Host},
			{Text: ymdhm(l.BeginAt)},
			{Text: end, Tone: toneIf(l.EndAt == nil, "good")},
			{Text: dur, Tone: "muted"},
		})
	}
	sec.Summary = fmt.Sprintf("%.0fh logged · %d sessions", total.Hours(), len(locs))
	return sec, true
}

func sessionDur(l snapshot.Location) (time.Duration, bool) {
	if l.EndAt == nil {
		return 0, false
	}
	begin, ok1 := parseTime(l.BeginAt)
	end, ok2 := parseTime(*l.EndAt)
	if !ok1 || !ok2 {
		return 0, false
	}
	return end.Sub(begin), true
}

func buildEvents(evs []snapshot.Event) (model.Section, bool) {
	if len(evs) == 0 {
		return model.Section{}, false
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].BeginAt > evs[j].BeginAt })

	sec := model.Section{Title: "Events", Count: len(evs), Columns: []string{"Event", "Kind", "When"}}
	for _, e := range evs {
		sec.Rows = append(sec.Rows, []model.Cell{
			{Text: e.Name},
			{Text: e.Kind, Tone: "muted"},
			{Text: ymd(e.BeginAt), Tone: "muted"},
		})
	}
	sec.Summary = fmt.Sprintf("%d event(s)", len(evs))
	return sec, true
}

func buildQuests(qs []snapshot.Quest) (model.Section, bool) {
	if len(qs) == 0 {
		return model.Section{}, false
	}
	validated := 0
	sec := model.Section{Title: "Quests", Count: len(qs), Columns: []string{"Quest", "Validated", "%"}}
	for _, q := range qs {
		v := model.Cell{Text: "—", Tone: "muted"}
		if q.ValidatedAt != nil {
			v, validated = model.Cell{Text: "✔ " + ymd(*q.ValidatedAt), Tone: "good"}, validated+1
		}
		sec.Rows = append(sec.Rows, []model.Cell{
			{Text: q.Name},
			v,
			{Text: dashFloat(q.Prct)},
		})
	}
	sec.Summary = fmt.Sprintf("%d quest(s) · %d validated", len(qs), validated)
	return sec, true
}

func buildTitles(titles []snapshot.Title) (model.Section, bool) {
	if len(titles) == 0 {
		return model.Section{}, false
	}
	sec := model.Section{Title: "Titles", Count: len(titles), Columns: []string{"Title", "Selected"}}
	for _, t := range titles {
		selected := model.Cell{}
		if t.Selected {
			selected = model.Cell{Text: "✔ selected", Tone: "good"}
		}
		sec.Rows = append(sec.Rows, []model.Cell{
			{Text: orDash(t.Name)},
			selected,
		})
	}
	sec.Summary = fmt.Sprintf("%d title(s)", len(titles))
	return sec, true
}

func buildCorrectionPoints(hs []snapshot.CorrectionPoint) (model.Section, bool) {
	if len(hs) == 0 {
		return model.Section{}, false
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i].CreatedAt > hs[j].CreatedAt })

	sec := model.Section{Title: "Correction points", Count: len(hs), Columns: []string{"Reason", "Δ", "Total", "When"}}
	for _, h := range hs {
		delta, tone := strconv.Itoa(h.Sum), "muted"
		switch {
		case h.Sum > 0:
			delta, tone = "+"+delta, "good"
		case h.Sum < 0:
			tone = "bad"
		}
		sec.Rows = append(sec.Rows, []model.Cell{
			{Text: h.Reason},
			{Text: delta, Tone: tone},
			{Text: strconv.Itoa(h.Total)},
			{Text: ymd(h.CreatedAt), Tone: "muted"},
		})
	}
	sec.Summary = fmt.Sprintf("%d change(s)", len(hs))
	return sec, true
}

func buildAchievements(p snapshot.Profile) (model.Section, bool) {
	if len(p.Achievements) == 0 {
		return model.Section{}, false
	}
	sec := model.Section{Title: "Achievements", Count: len(p.Achievements), Columns: []string{"Achievement", "Tier", "Description"}}
	for _, a := range p.Achievements {
		sec.Rows = append(sec.Rows, []model.Cell{
			{Text: a.Name},
			{Text: orDash(a.Tier), Tone: "muted"},
			{Text: a.Description, Tone: "muted"},
		})
	}
	sec.Summary = fmt.Sprintf("%d achievement(s)", len(p.Achievements))
	return sec, true
}
