//go:build e2e

package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cucumber/godog"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/tests/e2e/helpers"
)

type guestCtx struct {
	env          *helpers.Env
	eventID      string
	resp         *http.Response
	body         map[string]any
	visitorToken string
	entryID      string
	firstEntryID string
}

// RegisterGuest registers guest session step definitions.
func RegisterGuest(sc *godog.ScenarioContext, env *helpers.Env) {
	g := &guestCtx{env: env}

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if g.eventID != "" {
			_ = helpers.CleanupEvent(ctx, env.Pool, g.eventID)
			g.eventID = ""
		}
		g.resp, g.body, g.visitorToken, g.entryID, g.firstEntryID = nil, nil, "", "", ""
		return ctx, nil
	})

	sc.Step(`^a seeded guest event$`, g.seedEvent)
	sc.Step(`^a guest visitor joins anonymously$`, g.visitorJoins)
	sc.Step(`^a guest visitor joins with legacy NIK "([^"]*)" and email "([^"]*)"$`, g.visitorJoinsLegacy)
	sc.Step(`^visitor A has joined anonymously$`, g.visitorAJoins)
	sc.Step(`^the same visitor A joins again$`, g.sameVisitorJoins)
	sc.Step(`^the entry is the same as before$`, g.sameEntry)
	sc.Step(`^the response status is 200 or 201$`, g.statusIs2xx)
	sc.Step(`^the join response status is (\d+)$`, g.statusIs)
	sc.Step(`^a visitor token is returned$`, g.hasVisitorToken)
	sc.Step(`^the response body does not contain the NIK plaintext "([^"]*)"$`, g.nikNotInBody)
	sc.Step(`^the error code is "([^"]*)"$`, g.errorCodeIs)
}

func (g *guestCtx) seedEvent() error {
	eID, _, err := helpers.SeedEvent(context.Background(), g.env.Pool, 10)
	g.eventID = eID
	return err
}

// join posts the anonymous join (ADR-030). X-Client-Type: mobile makes Queue return visitorToken in the body (web gets
// the HttpOnly rb_visitor cookie). body is nil for the normal join; a legacy body is sent only to prove it is ignored.
func (g *guestCtx) join(body map[string]any, headers map[string]string) error {
	h := map[string]string{"X-Client-Type": "mobile"}
	for k, v := range headers {
		h[k] = v
	}
	var payload any
	if body != nil {
		payload = body
	}
	resp, err := helpers.PostJSON(context.Background(),
		helpers.Queue(g.env, "/api/v1/events/"+g.eventID+"/queue-entries"), payload, h)
	if err != nil {
		return err
	}
	g.resp = resp
	var decoded map[string]any
	_ = helpers.DecodeBody(resp, &decoded)
	g.body = decoded
	if data, ok := decoded["data"].(map[string]any); ok {
		if v, _ := data["visitorToken"].(string); v != "" {
			g.visitorToken = v
		}
		g.entryID, _ = data["entryId"].(string)
	}
	return nil
}

func (g *guestCtx) visitorJoins() error { return g.join(nil, nil) }

func (g *guestCtx) visitorJoinsLegacy(nik, email string) error {
	return g.join(map[string]any{"buyerNik": nik, "email": email, "emailConfirmation": email, "consentNik": true}, nil)
}

func (g *guestCtx) visitorAJoins() error {
	if err := g.join(nil, nil); err != nil {
		return err
	}
	if g.resp.StatusCode != 200 && g.resp.StatusCode != 201 {
		return fmt.Errorf("visitor A join failed with status %d", g.resp.StatusCode)
	}
	g.firstEntryID = g.entryID
	return nil
}

func (g *guestCtx) sameVisitorJoins() error {
	return g.join(nil, map[string]string{"X-Visitor-Token": g.visitorToken})
}

func (g *guestCtx) sameEntry() error {
	if g.firstEntryID == "" || g.entryID != g.firstEntryID {
		return fmt.Errorf("expected entry %q, got %q", g.firstEntryID, g.entryID)
	}
	return nil
}

func (g *guestCtx) statusIs2xx() error {
	if g.resp == nil {
		return fmt.Errorf("no response recorded")
	}
	if g.resp.StatusCode < 200 || g.resp.StatusCode >= 300 {
		return fmt.Errorf("expected 2xx, got %d", g.resp.StatusCode)
	}
	return nil
}

func (g *guestCtx) statusIs(expected int) error {
	if g.resp == nil {
		return fmt.Errorf("no response recorded")
	}
	if g.resp.StatusCode != expected {
		raw, _ := json.Marshal(g.body)
		return fmt.Errorf("expected %d, got %d: %s", expected, g.resp.StatusCode, raw)
	}
	return nil
}

func (g *guestCtx) hasVisitorToken() error {
	if g.visitorToken == "" {
		return fmt.Errorf("visitorToken missing from response data")
	}
	return nil
}

func (g *guestCtx) nikNotInBody(nik string) error {
	raw, _ := json.Marshal(g.body)
	if strings.Contains(string(raw), nik) {
		return fmt.Errorf("NIK plaintext %q found in response — PII leak", nik)
	}
	return nil
}

func (g *guestCtx) errorCodeIs(code string) error {
	got := helpers.ErrorCode(g.body)
	if got != code {
		raw, _ := json.Marshal(g.body)
		return fmt.Errorf("expected error code %q, got %q (body: %s)", code, got, raw)
	}
	return nil
}
