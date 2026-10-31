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
}

// RegisterGuest registers guest session step definitions.
func RegisterGuest(sc *godog.ScenarioContext, env *helpers.Env) {
	g := &guestCtx{env: env}

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if g.eventID != "" {
			_ = helpers.CleanupEvent(ctx, env.Pool, g.eventID)
			g.eventID = ""
		}
		g.resp, g.body, g.visitorToken = nil, nil, ""
		return ctx, nil
	})

	sc.Step(`^a seeded guest event$`, g.seedEvent)
	sc.Step(`^a guest visitor joins with NIK "([^"]*)" and email "([^"]*)"$`, g.visitorJoins)
	sc.Step(`^visitor A has joined with NIK "([^"]*)" and email "([^"]*)"$`, g.visitorAJoins)
	sc.Step(`^the same visitor A joins again with NIK "([^"]*)" and email "([^"]*)"$`, g.sameVisitorJoins)
	sc.Step(`^the response status is 200 or 201$`, g.statusIs2xx)
	sc.Step(`^the response status is (\d+)$`, g.statusIs)
	sc.Step(`^a visitor token is returned$`, g.hasVisitorToken)
	sc.Step(`^the response body does not contain the NIK plaintext "([^"]*)"$`, g.nikNotInBody)
	sc.Step(`^the error code is "([^"]*)"$`, g.errorCodeIs)
}

func (g *guestCtx) seedEvent() error {
	eID, _, err := helpers.SeedEvent(context.Background(), g.env.Pool, 10)
	g.eventID = eID
	return err
}

func (g *guestCtx) join(nik, email string, headers map[string]string) error {
	resp, err := helpers.PostJSON(context.Background(),
		helpers.Queue(g.env, "/api/v1/events/"+g.eventID+"/queue-entries"),
		map[string]any{
			"buyerNik":          nik,
			"email":             email,
			"emailConfirmation": email,
			"consentNik":        true,
		}, headers)
	if err != nil {
		return err
	}
	g.resp = resp
	g.body = nil
	var body map[string]any
	_ = helpers.DecodeBody(resp, &body)
	g.body = body
	if data, ok := body["data"].(map[string]any); ok {
		g.visitorToken, _ = data["visitorToken"].(string)
	}
	return nil
}

func (g *guestCtx) visitorJoins(nik, email string) error {
	return g.join(nik, email, nil)
}

func (g *guestCtx) visitorAJoins(nik, email string) error {
	if err := g.join(nik, email, nil); err != nil {
		return err
	}
	if g.resp.StatusCode != 200 && g.resp.StatusCode != 201 {
		return fmt.Errorf("visitor A join failed with status %d", g.resp.StatusCode)
	}
	return nil
}

func (g *guestCtx) sameVisitorJoins(nik, email string) error {
	headers := map[string]string{}
	if g.visitorToken != "" {
		headers["X-Visitor-Token"] = g.visitorToken
	}
	return g.join(nik, email, headers)
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
