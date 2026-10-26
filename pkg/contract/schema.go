// Package contract checks that an event payload follows its JSON Schema in docs/contracts/events. It is meant for
// producer and consumer tests (platform 12.2): a producer whose payload drifts from the schema fails its own test
// instead of failing in another service at runtime.
//
// It implements only the schema features the event contracts use: type (string, integer, number, boolean, array,
// object), required, properties, additionalProperties:false, items, enum, minimum, and the formats uuid,
// date-time, email, and int64. Anything else in a schema is ignored, not rejected.
package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/mail"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type schema struct {
	Type                 string             `json:"type"`
	Required             []string           `json:"required"`
	Properties           map[string]*schema `json:"properties"`
	AdditionalProperties json.RawMessage    `json:"additionalProperties"`
	Items                *schema            `json:"items"`
	Enum                 []any              `json:"enum"`
	Minimum              *float64           `json:"minimum"`
	Format               string             `json:"format"`
}

// ValidateFile validates payload (marshalled to JSON) against the schema stored at schemaPath.
func ValidateFile(schemaPath string, payload any) error {
	raw, err := os.ReadFile(schemaPath) // #nosec G304 -- test helper; the path is chosen by the test
	if err != nil {
		return fmt.Errorf("contract: baca schema: %w", err)
	}
	doc, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("contract: marshal payload: %w", err)
	}
	return Validate(raw, doc)
}

// Validate checks the JSON document doc against schemaJSON and reports every violation at once.
func Validate(schemaJSON, doc []byte) error {
	var s schema
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		return fmt.Errorf("contract: schema tidak terbaca: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("contract: dokumen bukan JSON: %w", err)
	}
	var errs []error
	s.check("$", v, &errs)
	return errors.Join(errs...)
}

func (s *schema) check(path string, v any, errs *[]error) {
	fail := func(format string, a ...any) {
		*errs = append(*errs, fmt.Errorf("%s: %s", path, fmt.Sprintf(format, a...)))
	}

	if !s.typeOK(v) {
		fail("harus bertipe %s, ditemukan %s", s.Type, jsonType(v))
		return
	}
	if len(s.Enum) > 0 && !inEnum(s.Enum, v) {
		fail("nilai %v di luar enum %v", v, s.Enum)
	}
	switch val := v.(type) {
	case string:
		if msg := formatError(s.Format, val); msg != "" {
			fail("%s", msg)
		}
	case json.Number:
		f, err := val.Float64()
		if err == nil && s.Minimum != nil && f < *s.Minimum {
			fail("%v kurang dari minimum %v", val, *s.Minimum)
		}
		if s.Format == "int64" {
			if _, err := val.Int64(); err != nil {
				fail("%v bukan int64", val)
			}
		}
	case map[string]any:
		for _, r := range s.Required {
			if _, ok := val[r]; !ok {
				fail("field wajib %q tidak ada", r)
			}
		}
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sub, known := s.Properties[k]
			switch {
			case known:
				sub.check(path+"."+k, val[k], errs)
			case string(bytes.TrimSpace(s.AdditionalProperties)) == "false":
				fail("field %q tidak ada di schema (additionalProperties=false)", k)
			}
		}
	case []any:
		if s.Items != nil {
			for i, item := range val {
				s.Items.check(fmt.Sprintf("%s[%d]", path, i), item, errs)
			}
		}
	}
}

func (s *schema) typeOK(v any) bool {
	switch s.Type {
	case "":
		return true
	case "string":
		_, ok := v.(string)
		return ok
	case "integer":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		f, err := n.Float64()
		return err == nil && f == math.Trunc(f) && !strings.ContainsAny(n.String(), ".eE")
	case "number":
		_, ok := v.(json.Number)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "null":
		return v == nil
	}
	return true
}

func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

func inEnum(enum []any, v any) bool {
	for _, e := range enum {
		if n, ok := v.(json.Number); ok {
			if f, ok := e.(float64); ok {
				if g, err := n.Float64(); err == nil && f == g {
					return true
				}
			}
			continue
		}
		if e == v {
			return true
		}
	}
	return false
}

func formatError(format, s string) string {
	switch format {
	case "uuid":
		if !uuidRe.MatchString(s) {
			return fmt.Sprintf("%q bukan UUID", s)
		}
	case "date-time":
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			return fmt.Sprintf("%q bukan date-time RFC 3339", s)
		}
	case "email":
		if a, err := mail.ParseAddress(s); err != nil || a.Address != s {
			return fmt.Sprintf("%q bukan email", s)
		}
	}
	return ""
}
