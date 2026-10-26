// Package templates embeds the email templates into the binary so no files are read at runtime.
package templates

import "embed"

// FS holds every email template: *.tmpl (plain text) and *.html (HTML variant).
//
//go:embed *.tmpl *.html
var FS embed.FS
