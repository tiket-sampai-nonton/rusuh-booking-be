// Package template implements ITemplateRenderer with text/template over embedded files.
package template

import (
	"fmt"
	"strings"
	"text/template"
	"time"

	templates "github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/assets/templates"
)

// Renderer renders named templates parsed once at construction.
type Renderer struct {
	tmpl *template.Template
}

// New parses every embedded *.tmpl with the shared funcmap. It fails fast if a template is malformed.
func New() (*Renderer, error) {
	t, err := template.New("notification").Funcs(funcMap()).ParseFS(templates.FS, "*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("template: parse: %w", err)
	}
	return &Renderer{tmpl: t}, nil
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
		"formatTime": func(t time.Time) string { return t.UTC().Format("02 Jan 2006 15:04 MST") },
		"formatIDR":  formatIDR,
	}
}

// formatIDR formats a minor-unit (cents) amount as thousands-separated rupiah, e.g. 3000000 → "30.000".
func formatIDR(minor int64) string {
	rupiah := minor / 100
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
