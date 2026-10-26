package usecase

import "github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/outbound"

// renderBoth renders the plain-text and HTML variants of one email; the text part stays as the fallback.
func renderBoth(tmpl outbound.ITemplateRenderer, name string, data any) (text, html string, err error) {
	if text, err = tmpl.Render(name, data); err != nil {
		return "", "", err
	}
	if html, err = tmpl.RenderHTML(name, data); err != nil {
		return "", "", err
	}
	return text, html, nil
}
