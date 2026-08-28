package view

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // registers the embedded IANA zone database time.LoadLocation reads
	"unicode"

	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// ----------------------------------------------------------------------------
// Small formatting helpers
// ----------------------------------------------------------------------------

// stars renders a 0–5 rating as filled/empty stars, clamped to range.
func stars(rating int) string {
	rating = max(0, min(5, rating))
	return strings.Repeat("★", rating) + strings.Repeat("☆", 5-rating)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func dashInt(p *int) string {
	if p == nil {
		return "-"
	}
	return strconv.Itoa(*p)
}

// CommaInt formats n with thousands separators for English locale.
func CommaInt(n int64) string {
	return message.NewPrinter(language.English).Sprintf("%d", n)
}

func dashFloat(p *float64) string {
	if p == nil {
		return "-"
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// ucFirst uppercases the first rune of s and leaves the rest unchanged.
func ucFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func toneIf(cond bool, tone string) string {
	if cond {
		return tone
	}
	return ""
}

// projectMarkTone reddens a project's failing final mark of 0 or below (e.g. a -42
// cheating penalty), matching how negative flags are flagged red; the Projects table
// shows pass/fail via its own Result column, so the mark itself only flags non-positive
// scores. A nil (missing) mark stays neutral. (Evals use evalMarkTone's pass-bar rule.)
func projectMarkTone(mark *int) string {
	return toneIf(mark != nil && *mark <= 0, "bad")
}

// A project's pass bar depends on its type: 50/100 for C-Piscine days, 80/100 for cursus
// projects. 42 publishes no per-project pass bar, so these are approximations: they fit
// where my own correction marks tended to split, close enough absent an official bar.
const piscinePassBar, cursusPassBar = 50, 80

func passBar(piscine bool) int {
	if piscine {
		return piscinePassBar
	}
	return cursusPassBar
}

// evalMarkTone reddens a mark below its project's pass bar (50 piscine / 80 cursus).
// Subsumes the old ≤0 case (0 < 50). A nil mark stays neutral.
func evalMarkTone(mark *int, piscine bool) string {
	return toneIf(mark != nil && *mark < passBar(piscine), "bad")
}

// ymd keeps just the date portion of an ISO-8601 timestamp, as dated in loc.
// A nil loc (or an unparseable timestamp) keeps the date as written.
func ymd(iso string, loc *time.Location) string {
	if loc != nil {
		if t, ok := parseTime(iso); ok {
			return t.In(loc).Format("2006-01-02")
		}
	}
	if len(iso) >= 10 {
		return iso[:10]
	}
	return iso
}

func parseTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)
	return t, err == nil
}

// ymdhm formats an ISO timestamp as "2006-01-02 15:04" in loc, falling back to the
// date. A nil loc keeps the timestamp's own offset (UTC, as 42 sends it).
func ymdhm(iso string, loc *time.Location) string {
	if t, ok := parseTime(iso); ok {
		if loc != nil {
			t = t.In(loc)
		}
		return t.Format("2006-01-02 15:04")
	}
	return ymd(iso, loc)
}

// campusLocation resolves a campus's IANA time zone (as 42 reports it, e.g.
// "Europe/Paris"). Absent or unknown zones yield nil, leaving times in UTC rather
// than mislabelling them as local. The zone database comes from this package's
// time/tzdata import, as the runtime image ships none of its own.
func campusLocation(tz string) *time.Location {
	if tz == "" {
		return nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil
	}
	return loc
}

func hoursMinutes(d time.Duration) string {
	h, m := int(d.Hours()), int(d.Minutes())%60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// FormatSyncTime formats a sync timestamp concisely for the profile/settings UI:
// "14:32" if it was today, otherwise "15/01/24".
func FormatSyncTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	t = t.UTC()
	now := time.Now().UTC()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("02/01/06")
}

// Ago formats a past time as a friendly relative duration: "just now",
// "5 minutes ago", "2 hours ago", or falls back to FormatSyncTime.
func Ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		m := int(d.Round(time.Minute).Minutes())
		return fmt.Sprintf("%d minute%s ago", m, plural(m))
	}
	if d < 24*time.Hour {
		h := int(d.Round(time.Hour).Hours())
		return fmt.Sprintf("%d hour%s ago", h, plural(h))
	}
	return FormatSyncTime(t)
}

// In formats a future duration as "in 5 minutes", "in 2 hours", etc.
func In(d time.Duration) string {
	if d < time.Minute {
		return "in less than a minute"
	}
	if d < time.Hour {
		m := int(d.Round(time.Minute).Minutes())
		return fmt.Sprintf("in %d minute%s", m, plural(m))
	}
	if d < 24*time.Hour {
		h := int(d.Round(time.Hour).Hours())
		return fmt.Sprintf("in %d hour%s", h, plural(h))
	}
	days := int(d.Round(24*time.Hour).Hours() / 24)
	return fmt.Sprintf("in %d day%s", days, plural(days))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
