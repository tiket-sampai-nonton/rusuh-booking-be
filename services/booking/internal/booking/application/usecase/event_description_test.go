package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// descriptionCases drives the Slate description validation through both usecases that accept it.
var descriptionCases = []struct {
	name    string
	raw     json.RawMessage
	invalid bool
}{
	{name: "absent", raw: nil},
	{name: "empty", raw: json.RawMessage{}},
	{name: "null clears", raw: json.RawMessage(`null`)},
	{name: "empty array", raw: json.RawMessage(`[]`)},
	{name: "valid nodes", raw: json.RawMessage(`[{"type":"paragraph","children":[{"text":"hi"}]}]`)},
	{name: "valid nodes with whitespace", raw: json.RawMessage(`[ {"type":"p"} , {"type":"h1"} ]`)},
	{name: "not json", raw: json.RawMessage(`{bad`), invalid: true},
	{name: "structured object", raw: json.RawMessage(`{"longDesc":"x","lineup":[],"terms":["a"]}`)},
	{name: "truncated object", raw: json.RawMessage(`{"longDesc":`), invalid: true},
	{name: "string instead of array", raw: json.RawMessage(`"hello"`), invalid: true},
	{name: "array of strings", raw: json.RawMessage(`["a"]`), invalid: true},
	{name: "array with null node", raw: json.RawMessage(`[{"type":"p"},null]`), invalid: true},
	{name: "array with number node", raw: json.RawMessage(`[{"type":"p"},1]`), invalid: true},
	{
		name:    "over 200 KB",
		raw:     json.RawMessage(`[{"text":"` + strings.Repeat("a", 200<<10) + `"}]`),
		invalid: true,
	},
}

func assertDescriptionError(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var ae *apperror.Error
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, inbound.CodeValidation, ae.Code)
	assert.Contains(t, ae.Details, "description")
}

func TestCreateEvent_Description(t *testing.T) {
	for _, c := range descriptionCases {
		t.Run(c.name, func(t *testing.T) {
			repo := &fakeEventWriteRepo{}
			uc := usecase.NewCreateEvent(repo, &fakeIDs{id: "ev-1"})
			id, err := uc.Create(context.Background(), dto.CreateEventRequest{
				Name:        "X",
				Description: c.raw,
				Categories:  []dto.CreateCategoryRequest{{Code: "A", Capacity: 10}},
			})
			if c.invalid {
				assertDescriptionError(t, err)
				assert.Empty(t, id)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "ev-1", id)
		})
	}
}

func TestUpdateEventConfig_Description(t *testing.T) {
	for _, c := range descriptionCases {
		t.Run(c.name, func(t *testing.T) {
			uc := usecase.NewUpdateEventConfig(&fakeEventWriteRepo{})
			err := uc.Update(context.Background(), "ev-1", "acct-7", dto.UpdateEventConfigRequest{Description: c.raw})
			if c.invalid {
				assertDescriptionError(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

// Exactly at the cap is accepted; the limit is exclusive of valid payloads at the boundary.
func TestUpdateEventConfig_DescriptionAtLimit(t *testing.T) {
	const prefix, suffix = `[{"text":"`, `"}]`
	pad := strings.Repeat("a", (200<<10)-len(prefix)-len(suffix))
	raw := json.RawMessage(prefix + pad + suffix)
	require.Len(t, raw, 200<<10)

	err := usecase.NewUpdateEventConfig(&fakeEventWriteRepo{}).
		Update(context.Background(), "ev-1", "acct-7", dto.UpdateEventConfigRequest{Description: raw})
	assert.NoError(t, err)
}
