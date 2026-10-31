//go:build livemail

package resend_test

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/qrcode"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/resend"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/template"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/outbound"
)

// TestLive_EticketThroughResend sends the real e-ticket (HTML + inline QR) with sample data to LIVE_EMAIL_TO.
// Run: set -a; . ./.env; set +a; LIVE_EMAIL_TO=you@example.com go test -tags livemail -run TestLive ./internal/notification/infrastructure/resend/
// It needs RESEND_API_KEY and EMAIL_FROM in the environment and sends one real email.
func TestLive_EticketThroughResend(t *testing.T) {
	to, key, from := os.Getenv("LIVE_EMAIL_TO"), os.Getenv("RESEND_API_KEY"), os.Getenv("EMAIL_FROM")
	if os.Getenv("PREVIEW_OUT") == "" && (to == "" || key == "" || from == "") {
		t.Skip("LIVE_EMAIL_TO, RESEND_API_KEY, dan EMAIL_FROM wajib diisi (atau PREVIEW_OUT untuk pratinjau saja)")
	}
	r, err := template.New()
	require.NoError(t, err)

	enc := qrcode.New()
	var tickets []dto.EticketTicket
	var images []outbound.InlineImage
	for i, c := range []struct{ code, name, nik, cat string }{
		{"RB-7K2M9QXA", "Budi Santoso", "3174********0001", "VIP"},
		{"RB-4P8W2NZD", "Sari Dewi", "3174********0002", "VIP"},
	} {
		payload := "RBT:" + c.code
		png, err := enc.PNG(payload)
		require.NoError(t, err)
		cid := "qr-" + string(rune('1'+i))
		images = append(images, outbound.InlineImage{CID: cid, ContentType: "image/png", Data: png})
		tickets = append(tickets, dto.EticketTicket{
			TicketInfo: dto.TicketInfo{TicketCode: c.code, AttendeeName: c.name, AttendeeNikMasked: c.nik, CategoryCode: c.cat},
			QRCID:      cid, QRPayload: payload,
		})
	}
	data := dto.EticketData{PublicRef: "RB-7K2M9Q", EventName: "Konser Rusuh Fest 2026", EventStartsAt: "2026-12-25T19:00:00Z",
		EventVenue: "Gelora Bung Karno", Tickets: tickets, TicketURL: "https://rusuhbooking.my.id/tickets/demo?t=contoh"}
	text, err := r.Render("eticket", data)
	require.NoError(t, err)
	html, err := r.RenderHTML("eticket", data)
	require.NoError(t, err)

	if out := os.Getenv("PREVIEW_OUT"); out != "" { // browser preview: swap cid: for data: URIs
		preview := html
		for _, im := range images {
			preview = strings.ReplaceAll(preview, "cid:"+im.CID, "data:"+im.ContentType+";base64,"+base64.StdEncoding.EncodeToString(im.Data))
		}
		require.NoError(t, os.WriteFile(out, []byte(preview), 0o600))
	}

	if to == "" || key == "" || from == "" {
		return // pratinjau saja
	}
	s := resend.New(resend.Config{APIKey: key, From: from, Timeout: 20 * time.Second})
	id, err := s.SendEmail(context.Background(), to, "[RBT] E-Tiket Anda — Konser Rusuh Fest 2026 (uji)", text, html, "live-"+time.Now().Format("150405"), images...)
	require.NoError(t, err)
	t.Logf("terkirim, id Resend: %s", id)
}

// TestLive_OTPThroughResend sends the login-code email (sample code) to LIVE_EMAIL_TO.
func TestLive_OTPThroughResend(t *testing.T) {
	to, key, from := os.Getenv("LIVE_EMAIL_TO"), os.Getenv("RESEND_API_KEY"), os.Getenv("EMAIL_FROM")
	if to == "" || key == "" || from == "" {
		t.Skip("LIVE_EMAIL_TO, RESEND_API_KEY, dan EMAIL_FROM wajib diisi")
	}
	r, err := template.New()
	require.NoError(t, err)
	data := dto.OTPData{Code: "482913", ExpiresAt: time.Now().Add(5 * time.Minute)}
	text, err := r.Render("otp_login", data)
	require.NoError(t, err)
	html, err := r.RenderHTML("otp_login", data)
	require.NoError(t, err)
	s := resend.New(resend.Config{APIKey: key, From: from, Timeout: 20 * time.Second})
	id, err := s.SendEmail(context.Background(), to, "Kode Login Anda (uji)", text, html, "live-otp-"+time.Now().Format("150405"))
	require.NoError(t, err)
	t.Logf("terkirim, id Resend: %s", id)
}
