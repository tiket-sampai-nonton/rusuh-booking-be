package contract_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/contract"
)

const sample = `{
  "type": "object", "additionalProperties": false,
  "required": ["id", "amount", "status", "at", "items"],
  "properties": {
    "id": {"type": "string", "format": "uuid"},
    "amount": {"type": "integer", "format": "int64", "minimum": 0},
    "status": {"type": "string", "enum": ["PAID", "FAILED"]},
    "at": {"type": "string", "format": "date-time"},
    "email": {"type": "string", "format": "email"},
    "items": {"type": "array", "items": {"type": "object", "required": ["code"], "properties": {"code": {"type": "string"}}}},
    "meta": {"type": "object"}
  }
}`

const valid = `{"id":"11111111-1111-1111-1111-111111111111","amount":1500000,"status":"PAID","at":"2026-10-26T10:00:00Z","items":[{"code":"A"}]}`

func TestValidate_AcceptsAConformingDocument(t *testing.T) {
	require.NoError(t, contract.Validate([]byte(sample), []byte(valid)))
	require.NoError(t, contract.Validate([]byte(sample), []byte(`{"id":"11111111-1111-1111-1111-111111111111","amount":0,"status":"FAILED","at":"2026-10-26T10:00:00+07:00","email":"a@b.id","items":[],"meta":{"x":1}}`)))
}

func TestValidate_ReportsEveryViolation(t *testing.T) {
	cases := map[string]struct{ doc, want string }{
		"field wajib hilang":   {`{"id":"11111111-1111-1111-1111-111111111111","amount":1,"status":"PAID","at":"2026-10-26T10:00:00Z"}`, `field wajib "items"`},
		"field di luar schema": {`{"id":"11111111-1111-1111-1111-111111111111","amount":1,"status":"PAID","at":"2026-10-26T10:00:00Z","items":[],"nik":"3174"}`, `field "nik" tidak ada`},
		"tipe salah":           {`{"id":"11111111-1111-1111-1111-111111111111","amount":"1","status":"PAID","at":"2026-10-26T10:00:00Z","items":[]}`, "$.amount: harus bertipe integer"},
		"bukan integer":        {`{"id":"11111111-1111-1111-1111-111111111111","amount":1.5,"status":"PAID","at":"2026-10-26T10:00:00Z","items":[]}`, "$.amount"},
		"di bawah minimum":     {`{"id":"11111111-1111-1111-1111-111111111111","amount":-1,"status":"PAID","at":"2026-10-26T10:00:00Z","items":[]}`, "kurang dari minimum"},
		"enum":                 {`{"id":"11111111-1111-1111-1111-111111111111","amount":1,"status":"REVIEW","at":"2026-10-26T10:00:00Z","items":[]}`, "di luar enum"},
		"uuid":                 {`{"id":"abc","amount":1,"status":"PAID","at":"2026-10-26T10:00:00Z","items":[]}`, "bukan UUID"},
		"date-time":            {`{"id":"11111111-1111-1111-1111-111111111111","amount":1,"status":"PAID","at":"2026-10-26 10:00:00","items":[]}`, "bukan date-time"},
		"email":                {`{"id":"11111111-1111-1111-1111-111111111111","amount":1,"status":"PAID","at":"2026-10-26T10:00:00Z","email":"bukan-email","items":[]}`, "bukan email"},
		"item array salah":     {`{"id":"11111111-1111-1111-1111-111111111111","amount":1,"status":"PAID","at":"2026-10-26T10:00:00Z","items":[{"nama":"x"}]}`, `$.items[0]: field wajib "code"`},
		"null untuk string":    {`{"id":null,"amount":1,"status":"PAID","at":"2026-10-26T10:00:00Z","items":[]}`, "$.id: harus bertipe string"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := contract.Validate([]byte(sample), []byte(c.doc))
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
		})
	}

	err := contract.Validate([]byte(sample), []byte(`{"amount":"x","extra":1}`))
	require.Error(t, err)
	for _, want := range []string{`"id"`, `"status"`, "$.amount", `"extra"`} {
		assert.Contains(t, err.Error(), want, "semua pelanggaran dilaporkan sekaligus")
	}
}

func TestValidate_BadInputs(t *testing.T) {
	assert.Error(t, contract.Validate([]byte(`{`), []byte(`{}`)))
	assert.Error(t, contract.Validate([]byte(sample), []byte(`bukan json`)))
}

func TestValidateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	require.NoError(t, os.WriteFile(path, []byte(sample), 0o600))
	type payload struct {
		ID     string `json:"id"`
		Amount int64  `json:"amount"`
		Status string `json:"status"`
		At     string `json:"at"`
		Items  []any  `json:"items"`
	}
	require.NoError(t, contract.ValidateFile(path, payload{ID: "11111111-1111-1111-1111-111111111111", Amount: 5, Status: "PAID", At: "2026-10-26T10:00:00Z", Items: []any{}}))
	assert.Error(t, contract.ValidateFile(path, payload{ID: "x"}))
	assert.Error(t, contract.ValidateFile(filepath.Join(t.TempDir(), "tidak-ada.json"), payload{}))
	assert.Error(t, contract.ValidateFile(path, make(chan int)))
}

// The defect platform 12.2 must catch: a producer that sends booking.journey.ended without the fields the schema requires.
func TestValidate_CatchesAProducerThatDriftedFromItsSchema(t *testing.T) {
	const journeyEnded = `{"type":"object","additionalProperties":false,"required":["eventId","queueEntryId","outcome"],
		"properties":{"eventId":{"type":"string","format":"uuid"},"queueEntryId":{"type":"string","format":"uuid"},"outcome":{"type":"string","enum":["PAID","EXPIRED"]}}}`
	err := contract.Validate([]byte(journeyEnded), []byte(`{"eventId":"11111111-1111-1111-1111-111111111111","outcome":"PAID","admissionId":"x"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"queueEntryId"`)
	assert.Contains(t, err.Error(), `"admissionId"`)
}
