package view

import (
	"testing"
	"time"
)

func TestFormatSyncTime(t *testing.T) {
	t.Parallel()
	if got := FormatSyncTime(time.Time{}); got != "" {
		t.Errorf("zero time: got %q, want empty", got)
	}

	now := time.Now().UTC()
	if got := FormatSyncTime(now); got != now.Format("15:04") {
		t.Errorf("today: got %q, want %q", got, now.Format("15:04"))
	}

	yesterday := now.Add(-24 * time.Hour)
	want := yesterday.Format("02/01/06")
	if got := FormatSyncTime(yesterday); got != want {
		t.Errorf("yesterday: got %q, want %q", got, want)
	}
}

func TestAgo(t *testing.T) {
	t.Parallel()
	now := time.Now()
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "just now"},
		{30 * time.Second, "just now"},
		{2 * time.Minute, "2 minutes ago"},
		{90 * time.Minute, "2 hours ago"},
	}
	for _, c := range cases {
		if got := Ago(now.Add(-c.d)); got != c.want {
			t.Errorf("Ago(%v): got %q, want %q", c.d, got, c.want)
		}
	}

	// Beyond 24 hours falls back to the calendar date.
	past := now.Add(-48 * time.Hour)
	if got, want := Ago(past), FormatSyncTime(past); got != want {
		t.Errorf("Ago(48h): got %q, want %q", got, want)
	}

	if got := Ago(time.Time{}); got != "" {
		t.Errorf("zero time: got %q, want empty", got)
	}
}

func TestIn(t *testing.T) {
	t.Parallel()
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "in less than a minute"},
		{30 * time.Second, "in less than a minute"},
		{2 * time.Minute, "in 2 minutes"},
		{90 * time.Minute, "in 2 hours"},
		{48 * time.Hour, "in 2 days"},
	}
	for _, c := range cases {
		if got := In(c.d); got != c.want {
			t.Errorf("In(%v): got %q, want %q", c.d, got, c.want)
		}
	}
}
