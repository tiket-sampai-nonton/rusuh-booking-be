package logger

import (
	"context"
	"log/slog"
	"time"
)

type ctxKey struct{}

// ContextWith returns a context whose attributes (key, value, ...) are added to every record logged with
// that context (slog.InfoContext dst). Dipakai middleware HTTP untuk requestId dan trace_id.
func ContextWith(ctx context.Context, args ...any) context.Context {
	prev, _ := ctx.Value(ctxKey{}).([]slog.Attr)
	attrs := make([]slog.Attr, len(prev), len(prev)+len(args)/2)
	copy(attrs, prev)
	r := slog.NewRecord(time.Time{}, slog.LevelInfo, "", 0)
	r.Add(args...)
	r.Attrs(func(a slog.Attr) bool { attrs = append(attrs, a); return true })
	return context.WithValue(ctx, ctxKey{}, attrs)
}

// ctxHandler menambahkan atribut dari context ke setiap record sebelum diteruskan ke handler asli.
type ctxHandler struct{ slog.Handler }

func (h ctxHandler) Handle(ctx context.Context, r slog.Record) error {
	if attrs, ok := ctx.Value(ctxKey{}).([]slog.Attr); ok {
		r.AddAttrs(attrs...)
	}
	return h.Handler.Handle(ctx, r)
}

func (h ctxHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return ctxHandler{h.Handler.WithAttrs(attrs)}
}

func (h ctxHandler) WithGroup(name string) slog.Handler {
	return ctxHandler{h.Handler.WithGroup(name)}
}
