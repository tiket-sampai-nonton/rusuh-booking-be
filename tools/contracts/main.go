// Command contracts memvalidasi kontrak di docs/contracts (platform R5.1): setiap OpenAPI harus 3.1 yang
// valid dan setiap schema event harus JSON Schema 2020-12 yang bisa dikompilasi. Dipakai `make contracts`
// dan job CI `contracts`.
//
//	go run . ../../docs/contracts
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const draft2020 = "https://json-schema.org/draft/2020-12/schema"

// Result adalah hasil pemeriksaan satu berkas (atau satu direktori yang kosong).
type Result struct {
	File string
	Err  error
}

// Check memeriksa openapi/*.yaml dan events/*.schema.json di bawah root.
func Check(root string) []Result {
	rs := checkOpenAPI(filepath.Join(root, "openapi"))
	_, schemaResults := loadSchemas(filepath.Join(root, "events"))
	return append(rs, schemaResults...)
}

func checkOpenAPI(dir string) []Result {
	files, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if len(files) == 0 {
		return []Result{{File: dir, Err: errors.New("tidak ada berkas openapi/*.yaml")}}
	}
	var rs []Result
	for _, f := range files {
		rs = append(rs, Result{File: f, Err: validateOpenAPI(f)})
	}
	return rs
}

func validateOpenAPI(path string) error {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false // kontrak harus berdiri sendiri; tidak ada fetch $ref ke luar berkas
	doc, err := loader.LoadFromFile(path)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.1") {
		return fmt.Errorf("versi OpenAPI %q, harus 3.1.x", doc.OpenAPI)
	}
	return doc.Validate(context.Background())
}

// Schemas menyimpan schema event yang sudah dikompilasi, berkunci nama berkas.
type Schemas map[string]*jsonschema.Schema

// LoadSchemas mengompilasi semua *.schema.json di dir; mengembalikan error bila ada yang gagal.
func LoadSchemas(dir string) (Schemas, error) {
	s, rs := loadSchemas(dir)
	var errs []error
	for _, r := range rs {
		if r.Err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.File, r.Err))
		}
	}
	return s, errors.Join(errs...)
}

func loadSchemas(dir string) (Schemas, []Result) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.schema.json"))
	if len(files) == 0 {
		return nil, []Result{{File: dir, Err: errors.New("tidak ada berkas events/*.schema.json")}}
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat() // uuid, date-time, dst. benar-benar dinilai, bukan sekadar anotasi

	out := Schemas{}
	var rs []Result
	for _, f := range files {
		sch, err := compile(c, f)
		if err == nil {
			out[filepath.Base(f)] = sch
		}
		rs = append(rs, Result{File: f, Err: err})
	}
	return out, rs
}

func compile(c *jsonschema.Compiler, path string) (*jsonschema.Schema, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var head struct {
		Schema string `json:"$schema"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("JSON tidak valid: %w", err)
	}
	if head.Schema != draft2020 {
		return nil, fmt.Errorf("$schema %q, harus JSON Schema 2020-12 (%s)", head.Schema, draft2020)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return c.Compile(abs)
}

// Validate memeriksa dokumen JSON terhadap schema bernama (mis. "envelope.schema.json").
func (s Schemas) Validate(name, doc string) error {
	sch, ok := s[name]
	if !ok {
		return fmt.Errorf("schema %q tidak ada", name)
	}
	v, err := jsonschema.UnmarshalJSON(strings.NewReader(doc))
	if err != nil {
		return fmt.Errorf("dokumen bukan JSON: %w", err)
	}
	return sch.Validate(v)
}

func main() {
	root := "docs/contracts"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	failed := 0
	for _, r := range Check(root) {
		if r.Err != nil {
			failed++
			fmt.Printf("FAIL %s: %v\n", r.File, r.Err)
			continue
		}
		fmt.Printf("ok   %s\n", r.File)
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "%d kontrak tidak valid\n", failed)
		os.Exit(1)
	}
}
