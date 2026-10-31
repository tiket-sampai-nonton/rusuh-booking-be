//go:build e2e

package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cucumber/godog"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/tests/e2e/helpers"
)

type userCtx struct {
	env         *helpers.Env
	eventID     string
	catID       string
	holdResult  helpers.HoldResult
	otpResp     *http.Response
	otpBody     map[string]any
	verifyResp  *http.Response
	verifyBody  map[string]any
}

// RegisterUser registers User Service and OTP step definitions.
func RegisterUser(sc *godog.ScenarioContext, env *helpers.Env) {
	u := &userCtx{env: env}

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if u.eventID != "" {
			_ = helpers.CleanupEvent(ctx, env.Pool, u.eventID)
			u.eventID = ""
		}
		u.otpResp, u.otpBody = nil, nil
		u.verifyResp, u.verifyBody = nil, nil
		return ctx, nil
	})

	sc.Step(`^a seeded user event with (\d+) seats$`, u.seedEvent)
	sc.Step(`^a guest user visitor joins and holds a ticket$`, u.guestJoinAndHold)
	sc.Step(`^the user hold response status is (\d+)$`, u.holdStatus)
	sc.Step(`^OTP is requested for "([^"]*)"$`, u.requestOTP)
	sc.Step(`^the OTP request status is (\d+)$`, u.otpRequestStatus)
	sc.Step(`^an OTP email arrives within (\d+) seconds for "([^"]*)"$`, u.otpEmailArrives)
	sc.Step(`^OTP has been requested for "([^"]*)"$`, u.requestOTP)
	sc.Step(`^wrong OTP "([^"]*)" is submitted for "([^"]*)"$`, u.submitWrongOTP)
	sc.Step(`^the OTP verify status is (\d+)$`, u.verifyStatus)
	sc.Step(`^the correct OTP from Mailpit is submitted for "([^"]*)"$`, u.submitCorrectOTP)
	sc.Step(`^access_token and refresh_token are present in the response$`, u.hasTokens)
}

func (u *userCtx) seedEvent(cap int) error {
	eID, catID, err := helpers.SeedEvent(context.Background(), u.env.Pool, cap)
	u.eventID = eID
	u.catID = catID
	return err
}

func (u *userCtx) guestJoinAndHold() error {
	admit, err := helpers.AdmitVisitor(u.env, u.eventID, "3174010101800001", "guest@example.test")
	if err != nil {
		return err
	}
	sessionID, err := helpers.CreateSession(u.env, admit.AdmissionToken, admit.VisitorToken)
	if err != nil {
		return err
	}
	u.holdResult = helpers.HoldTickets(u.env, sessionID, admit.VisitorToken,
		"user-guest-key-1",
		[]map[string]any{{"categoryId": u.catID, "attendee": map[string]string{"nik": "3174010101800001", "fullName": "Budi"}}})
	return nil
}

func (u *userCtx) holdStatus(expected int) error {
	if u.holdResult.StatusCode != expected {
		raw, _ := json.Marshal(u.holdResult.Body)
		return fmt.Errorf("expected %d, got %d: %s", expected, u.holdResult.StatusCode, raw)
	}
	return nil
}

func (u *userCtx) requestOTP(email string) error {
	resp, err := helpers.PostJSON(context.Background(),
		helpers.User(u.env, "/api/v1/auth/otp/request"),
		map[string]string{"email": email}, nil)
	if err != nil {
		return err
	}
	u.otpResp = resp
	u.otpBody = nil
	return helpers.DecodeBody(resp, &u.otpBody)
}

func (u *userCtx) otpRequestStatus(expected int) error {
	if u.otpResp == nil {
		return fmt.Errorf("no OTP request response recorded")
	}
	if u.otpResp.StatusCode != expected {
		raw, _ := json.Marshal(u.otpBody)
		return fmt.Errorf("expected %d, got %d: %s", expected, u.otpResp.StatusCode, raw)
	}
	return nil
}

func (u *userCtx) otpEmailArrives(_ int, email string) error {
	_, err := helpers.ReadOTP(context.Background(), u.env.MailpitAPI, email)
	return err
}

func (u *userCtx) submitWrongOTP(code, email string) error {
	resp, err := helpers.PostJSON(context.Background(),
		helpers.User(u.env, "/api/v1/auth/otp/verify"),
		map[string]string{"email": email, "code": code}, nil)
	if err != nil {
		return err
	}
	u.verifyResp = resp
	u.verifyBody = nil
	return helpers.DecodeBody(resp, &u.verifyBody)
}

func (u *userCtx) verifyStatus(expected int) error {
	if u.verifyResp == nil {
		return fmt.Errorf("no verify response recorded")
	}
	if u.verifyResp.StatusCode != expected {
		raw, _ := json.Marshal(u.verifyBody)
		return fmt.Errorf("expected %d, got %d: %s", expected, u.verifyResp.StatusCode, raw)
	}
	return nil
}

func (u *userCtx) submitCorrectOTP(email string) error {
	code, err := helpers.ReadOTP(context.Background(), u.env.MailpitAPI, email)
	if err != nil {
		return fmt.Errorf("read OTP from Mailpit: %w", err)
	}
	return u.submitWrongOTP(code, email)
}

func (u *userCtx) hasTokens() error {
	data, _ := u.verifyBody["data"].(map[string]any)
	if data["accessToken"] == nil || data["accessToken"] == "" {
		raw, _ := json.Marshal(u.verifyBody)
		return fmt.Errorf("accessToken missing from verify response: %s", raw)
	}
	if data["refreshToken"] == nil || data["refreshToken"] == "" {
		return fmt.Errorf("refreshToken missing from verify response")
	}
	return nil
}
