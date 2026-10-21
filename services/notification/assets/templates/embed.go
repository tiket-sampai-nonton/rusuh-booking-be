// Package templates embeds the email templates into the binary so no files are read at runtime.
package templates

import "embed"

// FS holds every *.tmpl email template.
//
//go:embed *.tmpl
var FS embed.FS
