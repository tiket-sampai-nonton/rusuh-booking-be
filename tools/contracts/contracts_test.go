package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const repoContracts = "../../docs/contracts"

func failures(rs []Result) []string {
	var out []string
	for _, r := range rs {
		if r.Err != nil {
			out = append(out, r.File+": "+r.Err.Error())
		}
	}
	return out
}

func TestRepoContractsAreValid(t *testing.T) {
	rs := Check(repoContracts)

	assert.Empty(t, failures(rs))
	var openapi, schemas int
	for _, r := range rs {
		switch {
		case strings.HasSuffix(r.File, ".yaml"):
			openapi++
		case strings.HasSuffix(r.File, ".schema.json"):
			schemas++
		}
	}
	assert.GreaterOrEqual(t, openapi, 7, "7 kontrak OpenAPI")
	assert.GreaterOrEqual(t, schemas, 12, "envelope + schema payload")
}

// write membuat pohon kontrak sementara: files = path relatif -> isi.
func write(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, body := range files {
		full := filepath.Join(root, p)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	}
	return root
}

const (
	okOpenAPI = "openapi: 3.1.0\ninfo:\n  title: t\n  version: '1'\npaths: {}\n"
	okSchema  = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`
)

func TestCheck_NegativeCases(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"YAML tidak valid", map[string]string{
			"openapi/a.yaml":       "openapi: 3.1.0\ninfo:\n  title: t\n  version: '1'\npaths: {}\nx:\n  description: Pada error \"KODE: pesan\" gagal\n",
			"events/e.schema.json": okSchema,
		}, "a.yaml"},
		{"OpenAPI bukan 3.1", map[string]string{
			"openapi/a.yaml":       strings.Replace(okOpenAPI, "3.1.0", "3.0.3", 1),
			"events/e.schema.json": okSchema,
		}, "3.1"},
		{"OpenAPI tidak valid (tanpa info)", map[string]string{
			"openapi/a.yaml":       "openapi: 3.1.0\npaths: {}\n",
			"events/e.schema.json": okSchema,
		}, "a.yaml"},
		{"schema JSON tidak valid", map[string]string{
			"openapi/a.yaml":       okOpenAPI,
			"events/e.schema.json": `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":5}`,
		}, "e.schema.json"},
		{"schema bukan 2020-12", map[string]string{
			"openapi/a.yaml":       okOpenAPI,
			"events/e.schema.json": `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object"}`,
		}, "2020-12"},
		{"schema tanpa $schema", map[string]string{
			"openapi/a.yaml":       okOpenAPI,
			"events/e.schema.json": `{"type":"object"}`,
		}, "2020-12"},
		{"OpenAPI dengan $ref ke URL eksternal ditolak", map[string]string{
			"openapi/a.yaml":       okOpenAPI + "components:\n  schemas:\n    X:\n      $ref: 'http://127.0.0.1:1/x.yaml#/X'\n",
			"events/e.schema.json": okSchema,
		}, "a.yaml"},
		{"tanpa berkas OpenAPI", map[string]string{"events/e.schema.json": okSchema}, "openapi"},
		{"tanpa berkas schema event", map[string]string{"openapi/a.yaml": okOpenAPI}, "events"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := failures(Check(write(t, tt.files)))
			require.NotEmpty(t, fs, "harus gagal")
			assert.Contains(t, strings.Join(fs, "\n"), tt.want)
		})
	}
}

func TestCheck_HappyPathMinimal(t *testing.T) {
	rs := Check(write(t, map[string]string{"openapi/a.yaml": okOpenAPI, "events/e.schema.json": okSchema}))
	assert.Empty(t, failures(rs))
	assert.Len(t, rs, 2)
}

func TestCheck_MissingDirectory(t *testing.T) {
	assert.NotEmpty(t, failures(Check(filepath.Join(t.TempDir(), "tidak-ada"))))
}

// envelopeOTP meniru keluaran User Service (pkg/rabbitmq.Envelope + payload otp_publisher.go).
func envelopeOTP(producer string) string {
	return `{
	  "messageId": "0b8f6a52-3c1e-4c2f-9d71-6a1c2f3a4b5c",
	  "eventType": "otp.requested",
	  "schemaVersion": 1,
	  "occurredAt": "2026-10-22T05:00:00Z",
	  "producer": "` + producer + `",
	  "aggregateId": "ab12cd34",
	  "payload": {"emailEnc": "QUJD", "codeEnc": "REVG", "expiresAt": "2026-10-22T05:05:00Z"}
	}`
}

func TestEnvelopeOTP(t *testing.T) {
	s, err := LoadSchemas(filepath.Join(repoContracts, "events"))
	require.NoError(t, err)

	t.Run("producer user-service lulus envelope dan payload", func(t *testing.T) {
		assert.NoError(t, s.Validate("envelope.schema.json", envelopeOTP("user-service")))
		assert.NoError(t, s.Validate("otp.requested.v1.schema.json", `{"emailEnc":"QUJD","codeEnc":"REVG","expiresAt":"2026-10-22T05:05:00Z"}`))
	})
	t.Run("producer di luar enum ditolak", func(t *testing.T) {
		assert.Error(t, s.Validate("envelope.schema.json", envelopeOTP("user")))
	})
	t.Run("payload OTP dengan plaintext email ditolak", func(t *testing.T) {
		assert.Error(t, s.Validate("otp.requested.v1.schema.json", `{"email":"a@b.c","emailEnc":"QUJD","codeEnc":"REVG","expiresAt":"2026-10-22T05:05:00Z"}`))
	})
	t.Run("messageId bukan uuid ditolak (format assertion aktif)", func(t *testing.T) {
		bad := strings.Replace(envelopeOTP("user-service"), "0b8f6a52-3c1e-4c2f-9d71-6a1c2f3a4b5c", "bukan-uuid", 1)
		assert.Error(t, s.Validate("envelope.schema.json", bad))
	})
	t.Run("schema tidak dikenal", func(t *testing.T) {
		assert.Error(t, s.Validate("tidak-ada.schema.json", `{}`))
	})
}
