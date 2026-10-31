//go:build e2e

package steps

import (
	"context"
	"fmt"
	"net/http"

	"github.com/cucumber/godog"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/tests/e2e/helpers"
)

type catalogCtx struct {
	env     *helpers.Env
	eventID string
	resp    *http.Response
	body    map[string]any
}

// RegisterCatalog registers catalog and event-detail step definitions.
func RegisterCatalog(sc *godog.ScenarioContext, env *helpers.Env) {
	c := &catalogCtx{env: env}

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if c.eventID != "" {
			_ = helpers.CleanupEvent(ctx, env.Pool, c.eventID)
			c.eventID = ""
		}
		c.resp, c.body = nil, nil
		return ctx, nil
	})

	sc.Step(`^I GET "([^"]*)" from booking service$`, c.doGET)
	sc.Step(`^the response status is (\d+)$`, c.statusIs)
	sc.Step(`^the response contains an events array$`, c.hasEventsArray)
	sc.Step(`^a seeded catalog event in city "([^"]*)"$`, c.seedEventInCity)
	sc.Step(`^a seeded catalog event with (\d+) tickets?$`, c.seedEventWithCapacity)
	sc.Step(`^all returned events have city "([^"]*)"$`, c.allHaveCity)
	sc.Step(`^I GET the catalog event detail$`, c.getEventDetail)
	sc.Step(`^the detail contains at least (\d+) category$`, c.detailHasCategories)
	sc.Step(`^category "([^"]*)" shows (\d+) available$`, c.categoryAvailability)
}

func (c *catalogCtx) doGET(path string) error {
	resp, err := helpers.GetJSON(context.Background(), helpers.Booking(c.env, path), nil)
	if err != nil {
		return err
	}
	c.resp = resp
	c.body = nil
	return helpers.DecodeBody(resp, &c.body)
}

func (c *catalogCtx) statusIs(expected int) error {
	if c.resp == nil {
		return fmt.Errorf("no response recorded")
	}
	if c.resp.StatusCode != expected {
		return fmt.Errorf("expected status %d, got %d", expected, c.resp.StatusCode)
	}
	return nil
}

func (c *catalogCtx) hasEventsArray() error {
	data, _ := c.body["data"].(map[string]any)
	_, ok := data["events"].([]any)
	if !ok {
		return fmt.Errorf("response.data.events is missing or not an array")
	}
	return nil
}

func (c *catalogCtx) seedEventInCity(city string) error {
	eID, _, err := helpers.SeedEvent(context.Background(), c.env.Pool, 10)
	if err != nil {
		return err
	}
	_, err = c.env.Pool.Exec(context.Background(),
		`UPDATE booking.events SET city=$1 WHERE id=$2::uuid`, city, eID)
	if err != nil {
		return err
	}
	c.eventID = eID
	return nil
}

func (c *catalogCtx) seedEventWithCapacity(cap int) error {
	eID, _, err := helpers.SeedEvent(context.Background(), c.env.Pool, cap)
	if err != nil {
		return err
	}
	c.eventID = eID
	return nil
}

func (c *catalogCtx) allHaveCity(city string) error {
	data, _ := c.body["data"].(map[string]any)
	events, _ := data["events"].([]any)
	for _, e := range events {
		ev, _ := e.(map[string]any)
		if ev["city"] != city {
			return fmt.Errorf("event %v has city %v, want %v", ev["eventId"], ev["city"], city)
		}
	}
	return nil
}

func (c *catalogCtx) getEventDetail() error {
	if c.eventID == "" {
		return fmt.Errorf("no event seeded")
	}
	resp, err := helpers.GetJSON(context.Background(),
		helpers.Booking(c.env, "/api/v1/events/"+c.eventID), nil)
	if err != nil {
		return err
	}
	c.resp = resp
	c.body = nil
	return helpers.DecodeBody(resp, &c.body)
}

func (c *catalogCtx) detailHasCategories(n int) error {
	data, _ := c.body["data"].(map[string]any)
	cats, _ := data["categories"].([]any)
	if len(cats) < n {
		return fmt.Errorf("expected >= %d categories, got %d", n, len(cats))
	}
	return nil
}

func (c *catalogCtx) categoryAvailability(code string, expected int) error {
	data, _ := c.body["data"].(map[string]any)
	cats, _ := data["categories"].([]any)
	for _, cat := range cats {
		cm, _ := cat.(map[string]any)
		if cm["code"] == code {
			avail, _ := cm["available"].(float64)
			if int(avail) != expected {
				return fmt.Errorf("category %s: expected %d available, got %d", code, expected, int(avail))
			}
			return nil
		}
	}
	return fmt.Errorf("category %s not found in response", code)
}
