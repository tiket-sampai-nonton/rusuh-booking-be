//go:build e2e

package e2e_test

import (
	"context"
	"testing"

	"github.com/cucumber/godog"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/tests/e2e/helpers"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/tests/e2e/steps"
)

func TestE2E(t *testing.T) {
	env, err := helpers.NewEnv(context.Background())
	if err != nil {
		t.Fatalf("env setup: %v", err)
	}
	t.Cleanup(env.Pool.Close)

	suite := godog.TestSuite{
		Name: "e2e",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			steps.RegisterCatalog(sc, env)
			steps.RegisterGuest(sc, env)
			steps.RegisterQueue(sc, env)
			steps.RegisterBooking(sc, env)
			steps.RegisterEdit(sc, env)
			steps.RegisterCancel(sc, env)
			steps.RegisterUser(sc, env)
		},
		Options: &godog.Options{
			Format:        "pretty",
			Paths:         []string{"../../tests/features"},
			TestingT:      t,
			StopOnFailure: false,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("e2e: one or more scenarios failed")
	}
}
