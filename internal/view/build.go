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
// public or private. The keys match the Build() includeSection calls and the
// snapshot resource names.
var ToggleableSections = []struct{ Key, Label string }{
	{"points", "Points remaining"},
	{"coalitions", "Coalition"},
	{"contact", "Contact"},
	{"projects_users", "Projects"},
	{"scale_teams_as_corrected", "Evaluations received"},
	{"scale_teams_as_corrector", "Evaluations given"},
	{"correction_point_historics", "Correction points"},
	{"quests_users", "Quests"},
	{"titles_users", "Titles"},
	{"achievements", "Achievements"},
	{"events_users", "Events"},
	{"locations", "Locations"},
	{"skills", "Skills"},
}

// sectionPrivateByDefault lists sections hidden from non-owners unless the owner
// explicitly opts them public.
var sectionPrivateByDefault = map[string]bool{
	"coalitions":                 true,
	"locations":                  true,
	"skills":                     true,
	"contact":                    true,
	"points":                     true,
	"correction_point_historics": true,
	"events_users":               true,
}

// SectionPublic reports whether a section is visible to non-owners, honouring the
// account's explicit overrides over the built-in defaults.
func SectionPublic(vis map[string]bool, key string) bool {
	if v, ok := vis[key]; ok {
		return v
	}
	return !sectionPrivateByDefault[key]
}

// includeSection sets a panel pointer when the section has data and the viewer
// may see it. The owner also sees private sections, badged so they know what
// others can't.
func includeSection[T any](dst **T, key string, owner bool, vis map[string]bool, build func() (T, bool), setPrivate func(*T, bool)) {
	public := SectionPublic(vis, key)
	sec, ok := build()
	if !ok || !(owner || public) {
		return
	}
	setPrivate(&sec, owner && !public)
	*dst = &sec
}

// Build assembles the dashboard from a curated snapshot. When owner is false the
// Email row and any non-public section (per vis) are dropped.
func Build(snaps map[string]json.RawMessage, owner bool, vis map[string]bool) model.PageData {
	var me snapshot.Profile
	if err := json.Unmarshal(snaps["me"], &me); err != nil {
		return model.PageData{IsError: true, Status: fmt.Sprintf("stored \"me\" snapshot is not valid JSON: %v", err)}
	}

	d := model.PageData{
		Profile: buildProfile(me, load[snapshot.Coalition](snaps, "coalitions"), owner, vis),
	}

	includeSection(&d.Sections.Contact, "contact", owner, vis, func() (model.ContactSection, bool) { return buildContact(me) },
		func(s *model.ContactSection, p bool) { s.Private = p })

	includeSection(&d.Sections.Projects, "projects_users", owner, vis, func() (model.TableSection, bool) {
		return buildProjects(load[snapshot.Project](snaps, "projects_users"))
	}, func(s *model.TableSection, p bool) { s.Private = p })

	includeSection(&d.Sections.EvalsReceived, "scale_teams_as_corrected", owner, vis, func() (model.EvalSection, bool) {
		return buildEvals("Evaluations received", false, load[snapshot.Eval](snaps, "scale_teams_as_corrected"))
	}, func(s *model.EvalSection, p bool) { s.Private = p })

	includeSection(&d.Sections.EvalsGiven, "scale_teams_as_corrector", owner, vis, func() (model.EvalSection, bool) {
		return buildEvals("Evaluations given", true, load[snapshot.Eval](snaps, "scale_teams_as_corrector"))
	}, func(s *model.EvalSection, p bool) { s.Private = p })

	includeSection(&d.Sections.CorrectionPoints, "correction_point_historics", owner, vis, func() (model.TableSection, bool) {
		return buildCorrectionPoints(load[snapshot.CorrectionPoint](snaps, "correction_point_historics"))
	}, func(s *model.TableSection, p bool) { s.Private = p })

	includeSection(&d.Sections.Quests, "quests_users", owner, vis, func() (model.TableSection, bool) {
		return buildQuests(load[snapshot.Quest](snaps, "quests_users"))
	}, func(s *model.TableSection, p bool) { s.Private = p })

	includeSection(&d.Sections.Titles, "titles_users", owner, vis, func() (model.TableSection, bool) {
		return buildTitles(load[snapshot.Title](snaps, "titles_users"), me.Login)
	}, func(s *model.TableSection, p bool) { s.Private = p })

	includeSection(&d.Sections.Achievements, "achievements", owner, vis, func() (model.TableSection, bool) { return buildAchievements(me) },
		func(s *model.TableSection, p bool) { s.Private = p })

	includeSection(&d.Sections.Events, "events_users", owner, vis, func() (model.TableSection, bool) {
		return buildEvents(load[snapshot.Event](snaps, "events_users"))
	}, func(s *model.TableSection, p bool) { s.Private = p })

	includeSection(&d.Sections.Locations, "locations", owner, vis, func() (model.TableSection, bool) {
		return buildLocations(load[snapshot.Location](snaps, "locations"))
	}, func(s *model.TableSection, p bool) { s.Private = p })

	includeSection(&d.Sections.Skills, "skills", owner, vis, func() (model.SkillsSection, bool) { return buildSkills(me) },
		func(s *model.SkillsSection, p bool) { s.Private = p })

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

func buildProfile(p snapshot.Profile, coalitions []snapshot.Coalition, owner bool, vis map[string]bool) *model.Profile {
	prof := &model.Profile{
		Name:     cmp.Or(p.Name, "Unknown"),
		Login:    p.Login,
		ImageURL: p.ImageURL,
	}

	if p.Campus != "" {
		prof.PrimaryStats = append(prof.PrimaryStats, model.KV{Key: "Campus", Value: p.Campus})
	}
	pool := strings.TrimSpace(p.PoolMonth + " " + p.PoolYear)
	if pool != "" {
		prof.PrimaryStats = append(prof.PrimaryStats, model.KV{Key: "Selection pool", Value: ucFirst(pool)})
	}

	points := &model.PointsCard{Eval: p.CorrectionPoint, Wallet: p.Wallet}
	public := SectionPublic(vis, "points")
	if owner || public {
		points.Private = owner && !public
		prof.Points = points
	}

	cursus := append([]snapshot.Cursus(nil), p.Cursus...)
	sort.Slice(cursus, func(i, j int) bool { return cursus[i].Level > cursus[j].Level })
	for i, cu := range cursus {
		prof.Cursus = append(prof.Cursus, model.CursusRow{
			Name:   cu.Name,
			Grade:  orDash(cu.Grade),
			Level:  fmt.Sprintf("Level %.2f", cu.Level),
			Latest: i == 0,
		})
	}

	public = SectionPublic(vis, "coalitions")
	if (owner || public) && len(coalitions) > 0 {
		c := coalitions[0]
		prof.Coalition = &model.CoalitionBadge{
			Name:    c.Name,
			Score:   CommaInt(int64(c.Score)),
			Color:   cmp.Or(c.Color, "#00babc"),
			Private: owner && !public,
		}
	}
	return prof
}

func buildContact(p snapshot.Profile) (model.ContactSection, bool) {
	if p.Email == "" {
		return model.ContactSection{}, false
	}
	return model.ContactSection{PanelHeader: model.PanelHeader{Title: "Contact"}, Email: p.Email}, true
}

func buildSkills(p snapshot.Profile) (model.SkillsSection, bool) {
	if len(p.Cursus) == 0 {
		return model.SkillsSection{}, false
	}
	skills := topSkills(p.Cursus)
	if len(skills) == 0 {
		return model.SkillsSection{}, false
	}
	return model.SkillsSection{
		PanelHeader: model.PanelHeader{Title: "Skills", Count: len(skills)},
		Skills:      skills,
	}, true
}

// topSkills aggregates skills across cursus (taking each skill's highest level),
// returns all of them, and scales each bar relative to the strongest skill.
func topSkills(cursus []snapshot.Cursus) []model.SkillBar {
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
	for name, level := range best {
		all = append(all, skill{name, level})
		top = max(top, level)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].level > all[j].level })

	bars := make([]model.SkillBar, len(all))
	for i, s := range all {
		pct := 100
		if top > 0 {
			pct = int(s.level / top * 100)
		}
		bars[i] = model.SkillBar{
			Name:  s.name,
			Level: fmt.Sprintf("%.2f", s.level),
			Pct:   pct,
			Index: i + 1,
		}
	}
	return bars
}

func buildProjects(ps []snapshot.Project) (model.TableSection, bool) {
	if len(ps) == 0 {
		return model.TableSection{}, false
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].When > ps[j].When })

	passed, failed := 0, 0
	sec := model.TableSection{
		PanelHeader: model.PanelHeader{Title: "Projects", Count: len(ps)},
		Columns:     []string{"Project", "Mark", "Result", "When"},
	}
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
func buildEvals(title string, given bool, evs []snapshot.Eval) (model.EvalSection, bool) {
	if len(evs) == 0 {
		return model.EvalSection{}, false
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].BeginAt > evs[j].BeginAt })

	sec := model.EvalSection{PanelHeader: model.PanelHeader{Title: title, Count: len(evs)}}
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

func buildLocations(locs []snapshot.Location) (model.TableSection, bool) {
	if len(locs) == 0 {
		return model.TableSection{}, false
	}
	sort.Slice(locs, func(i, j int) bool { return locs[i].BeginAt > locs[j].BeginAt })

	var total time.Duration
	for _, l := range locs {
		if d, ok := sessionDur(l); ok {
			total += d
		}
	}

	sec := model.TableSection{
		PanelHeader: model.PanelHeader{Title: "Locations", Count: len(locs)},
		Columns:     []string{"Host", "Begin", "End", "Duration"},
	}
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

func buildEvents(evs []snapshot.Event) (model.TableSection, bool) {
	if len(evs) == 0 {
		return model.TableSection{}, false
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].BeginAt > evs[j].BeginAt })

	sec := model.TableSection{
		PanelHeader: model.PanelHeader{Title: "Events", Count: len(evs)},
		Columns:     []string{"Event", "Kind", "When"},
	}
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

func buildQuests(qs []snapshot.Quest) (model.TableSection, bool) {
	if len(qs) == 0 {
		return model.TableSection{}, false
	}
	validated := 0
	sec := model.TableSection{
		PanelHeader: model.PanelHeader{Title: "Quests", Count: len(qs)},
		Columns:     []string{"Quest", "Validated", "%"},
	}
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

func buildTitles(titles []snapshot.Title, login string) (model.TableSection, bool) {
	if len(titles) == 0 {
		return model.TableSection{}, false
	}
	sec := model.TableSection{
		PanelHeader: model.PanelHeader{Title: "Titles", Count: len(titles)},
		Columns:     []string{"Title", "Selected"},
	}
	for _, t := range titles {
		selected := model.Cell{}
		if t.Selected {
			selected = model.Cell{Text: "✔ selected", Tone: "good"}
		}
		sec.Rows = append(sec.Rows, []model.Cell{
			{Text: orDash(strings.ReplaceAll(t.Name, "%login", login))},
			selected,
		})
	}
	sec.Summary = fmt.Sprintf("%d title(s)", len(titles))
	return sec, true
}

func buildCorrectionPoints(hs []snapshot.CorrectionPoint) (model.TableSection, bool) {
	if len(hs) == 0 {
		return model.TableSection{}, false
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i].CreatedAt > hs[j].CreatedAt })

	sec := model.TableSection{
		PanelHeader: model.PanelHeader{Title: "Correction points", Count: len(hs)},
		Columns:     []string{"Reason", "Δ", "Total", "When"},
	}
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

func buildAchievements(p snapshot.Profile) (model.TableSection, bool) {
	if len(p.Achievements) == 0 {
		return model.TableSection{}, false
	}
	sec := model.TableSection{
		PanelHeader: model.PanelHeader{Title: "Achievements", Count: len(p.Achievements)},
		Columns:     []string{"Achievement", "Tier", "Description"},
	}
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
