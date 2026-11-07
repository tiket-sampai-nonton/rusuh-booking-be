package postgres

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
)

func TestDescriptionArg(t *testing.T) {
	for name, c := range map[string]struct {
		in   json.RawMessage
		want any
	}{
		"nil is SQL NULL":        {nil, nil},
		"empty is SQL NULL":      {json.RawMessage{}, nil},
		"JSON null is SQL NULL":  {json.RawMessage(`null`), nil},
		"empty array is kept":    {json.RawMessage(`[]`), `[]`},
		"document is passed raw": {json.RawMessage(`[{"type":"p"}]`), `[{"type":"p"}]`},
	} {
		assert.Equal(t, c.want, descriptionArg(c.in), name)
	}
}

func TestBuildUpdateConfig(t *testing.T) {
	sel, edit := 120, true
	doc := json.RawMessage(`[{"type":"p"}]`)
	for name, c := range map[string]struct {
		req      dto.UpdateEventConfigRequest
		wantSQL  string
		wantArgs []any
	}{
		"nothing to change": {dto.UpdateEventConfigRequest{}, "", nil},
		"description only": {
			dto.UpdateEventConfigRequest{Description: doc},
			`UPDATE booking.events SET description = $2::jsonb WHERE id = $1::uuid`,
			[]any{"ev", `[{"type":"p"}]`},
		},
		"JSON null clears the column": {
			dto.UpdateEventConfigRequest{Description: json.RawMessage(`null`)},
			`UPDATE booking.events SET description = $2::jsonb WHERE id = $1::uuid`,
			[]any{"ev", nil},
		},
		"description placeholder follows the other fields": {
			dto.UpdateEventConfigRequest{SelectionTTLSeconds: &sel, EditEnabled: &edit, Description: doc},
			`UPDATE booking.events SET selection_ttl_seconds = $2, selection_edit_enabled = $3, description = $4::jsonb WHERE id = $1::uuid`,
			[]any{"ev", 120, true, `[{"type":"p"}]`},
		},
		"without description it is untouched": {
			dto.UpdateEventConfigRequest{SelectionTTLSeconds: &sel},
			`UPDATE booking.events SET selection_ttl_seconds = $2 WHERE id = $1::uuid`,
			[]any{"ev", 120},
		},
	} {
		gotSQL, gotArgs := buildUpdateConfig("ev", c.req)
		assert.Equal(t, c.wantSQL, gotSQL, name)
		assert.Equal(t, c.wantArgs, gotArgs, name)
	}
}

// fakeRow records the Scan destinations and fills the description destination.
type fakeRow struct {
	dest []any
}

func (f *fakeRow) Scan(dest ...any) error {
	f.dest = dest
	if b, ok := dest[len(dest)-1].(*[]byte); ok {
		*b = []byte(`[{"type":"p"}]`)
	}
	return nil
}

// scanEvent appends extra destinations after the base columns and the optional minPrice, so GetByID can
// read description without adding it to the catalog-list columns.
func TestScanEvent_ExtraDestinationsFollowBaseAndMinPrice(t *testing.T) {
	base := &fakeRow{}
	_, err := scanEvent(base, nil)
	assert.NoError(t, err)
	n := len(base.dest)

	var min int64
	withMin := &fakeRow{}
	_, err = scanEvent(withMin, &min)
	assert.NoError(t, err)
	assert.Len(t, withMin.dest, n+1)

	var desc []byte
	withExtra := &fakeRow{}
	_, err = scanEvent(withExtra, nil, &desc)
	assert.NoError(t, err)
	assert.Len(t, withExtra.dest, n+1)
	assert.Equal(t, `[{"type":"p"}]`, string(desc))

	both := &fakeRow{}
	_, err = scanEvent(both, &min, &desc)
	assert.NoError(t, err)
	assert.Len(t, both.dest, n+2)
	assert.Same(t, &min, both.dest[n])
}
