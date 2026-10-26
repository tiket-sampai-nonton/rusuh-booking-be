package outbound

//go:generate go tool mockgen -source=template_renderer.go -destination=../../../../_mock/notification/port/outbound/template_renderer_mock.go -package=mock_outbound

// ITemplateRenderer renders a named email template with the given data. A render error is
// permanent (poison → DLQ).
type ITemplateRenderer interface {
	Render(name string, data any) (string, error)
	// RenderHTML renders the HTML variant (html/template, so buyer-supplied values are escaped).
	RenderHTML(name string, data any) (string, error)
}
