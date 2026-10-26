// Package template implements ITemplateRenderer with text/template over embedded files.
package template

import (
	"fmt"
	htmltemplate "html/template"
	"strings"
	"text/template"
	"time"

	templates "github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/assets/templates"
)

// Renderer renders named templates parsed once at construction.
type Renderer struct {
	tmpl *template.Template
	html *htmltemplate.Template
}

// New parses every embedded *.tmpl with the shared funcmap. It fails fast if a template is malformed.
func New() (*Renderer, error) {
	t, err := template.New("notification").Funcs(funcMap()).ParseFS(templates.FS, "*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("template: parse: %w", err)
	}
	h, err := htmltemplate.New("notification-html").Funcs(funcMap()).ParseFS(templates.FS, "*.html")
	if err != nil {
		return nil, fmt.Errorf("template: parse html: %w", err)
	}
	return &Renderer{tmpl: t, html: h}, nil
}

// RenderHTML executes the html/template named name (file name.html) so every value is escaped for HTML.
func (r *Renderer) RenderHTML(name string, data any) (string, error) {
	var sb strings.Builder
	if err := r.html.ExecuteTemplate(&sb, name+".html", data); err != nil {
		return "", fmt.Errorf("template: render html %s: %w", name, err)
	}
	return sb.String(), nil
}

// Render executes the template named name (without extension) with data.
func (r *Renderer) Render(name string, data any) (string, error) {
	var sb strings.Builder
	if err := r.tmpl.ExecuteTemplate(&sb, name+".tmpl", data); err != nil {
		return "", fmt.Errorf("template: render %s: %w", name, err)
	}
	return sb.String(), nil
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"formatTime":  func(t time.Time) string { return t.UTC().Format("02 Jan 2006 15:04 MST") },
		"formatIDR":   formatIDR,
		"formatStart": formatStart,
		"formatClock": func(t time.Time) string { return t.In(wib).Format("15.04") + " WIB" },
	}
}

// formatIDR formats an amount as thousands-separated rupiah, e.g. 3000000 → "3.000.000". In this system one IDR
// minor unit is one rupiah (Booking stores price_minor = 1500000 for Rp 1.500.000; IDR has no decimals), so nothing is divided.
func formatIDR(minor int64) string {
	rupiah := minor
	neg := rupiah < 0
	if neg {
		rupiah = -rupiah
	}
	s := fmt.Sprintf("%d", rupiah)
	var out strings.Builder
	n := len(s)
	for i, c := range s {
		if i > 0 && (n-i)%3 == 0 {
			out.WriteByte('.')
		}
		out.WriteRune(c)
	}
	if neg {
		return "-" + out.String()
	}
	return out.String()
}

var wib = time.FixedZone("WIB", 7*60*60)

var months = [...]string{"Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"}

// formatStart turns an RFC 3339 start time into "26 Des 2026, 02.00 WIB". The event payload carries no time zone, so WIB is
// assumed (shortcut: events outside WIB would need the zone in the booking.paid payload). An unparsable value is shown as is.
func formatStart(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	t = t.In(wib)
	return fmt.Sprintf("%d %s %d, %02d.%02d WIB", t.Day(), months[t.Month()-1], t.Year(), t.Hour(), t.Minute())
}
