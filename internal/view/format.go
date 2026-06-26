package view

import (
	"fmt"
	"strconv"
	"strings"
	"time"
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

// markTone reddens a failing final mark of 0 or below (e.g. a -42 cheating
// penalty), matching how negative flags are flagged red. A nil (missing) mark
// stays neutral.
func markTone(mark *int) string {
	return toneIf(mark != nil && *mark <= 0, "bad")
}

// ymd keeps just the date portion of an ISO-8601 timestamp.
func ymd(iso string) string {
	if len(iso) >= 10 {
		return iso[:10]
	}
	return iso
}

func parseTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)
	return t, err == nil
}

// ymdhm formats an ISO timestamp as "2006-01-02 15:04", falling back to the date.
func ymdhm(iso string) string {
	if t, ok := parseTime(iso); ok {
		return t.Format("2006-01-02 15:04")
	}
	return ymd(iso)
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
