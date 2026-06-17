package view

import (
	"fmt"
	"strconv"
	"strings"
	"time"
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
		return "—"
	}
	return strconv.Itoa(*p)
}

func dashFloat(p *float64) string {
	if p == nil {
		return "—"
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
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
