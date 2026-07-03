// Package viewctx carries request-scoped values from the web middleware to the
// view templates through context - currently just the resolved page Theme, read by
// the shared Layout to set <html data-theme>.
package viewctx

import "context"

// Theme is the resolved page theme for a request: an explicit override, or
// ThemeAuto to defer to the OS (the CSS prefers-color-scheme media query).
type Theme string

const (
	ThemeAuto  Theme = "" // no override, follow the OS
	ThemeLight Theme = "light"
	ThemeDark  Theme = "dark"
)

// ParseTheme maps a stored/form preference string to a Theme, normalising any
// unknown or empty value to ThemeAuto. The single source of what's a valid override.
func ParseTheme(s string) Theme {
	switch Theme(s) {
	case ThemeLight, ThemeDark:
		return Theme(s)
	default:
		return ThemeAuto
	}
}

// IsOverride reports whether the theme should be pinned via <html data-theme>
// (vs. left to the OS).
func (t Theme) IsOverride() bool { return t == ThemeLight || t == ThemeDark }

type themeKey struct{}

// WithTheme returns a context carrying the resolved page theme.
func WithTheme(ctx context.Context, t Theme) context.Context {
	return context.WithValue(ctx, themeKey{}, t)
}

// FromContext returns the resolved page theme, or ThemeAuto when none is set.
func FromContext(ctx context.Context) Theme {
	t, _ := ctx.Value(themeKey{}).(Theme)
	return t
}
