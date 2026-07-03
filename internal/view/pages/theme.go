package pages

import (
	"context"

	"github.com/EvAvKein/Fortytwode/internal/view/viewctx"
	"github.com/a-h/templ"
)

// themeAttr renders the data-theme attribute on <html> only when the request
// carries an explicit light/dark override (see viewctx, set by the web
// themeContext middleware). Otherwise it renders nothing and the CSS
// prefers-color-scheme media query picks the theme from the visitor's OS.
func themeAttr(ctx context.Context) templ.Attributes {
	if t := viewctx.FromContext(ctx); t.IsOverride() {
		return templ.Attributes{"data-theme": string(t)}
	}
	return nil
}
