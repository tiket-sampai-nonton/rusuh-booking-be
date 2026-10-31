//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pb33f/libopenapi"
	validator "github.com/pb33f/libopenapi-validator"
)

// Every request the journey makes is recorded and, once all tests ran, checked against docs/contracts/openapi:
// the specification must describe what the services actually answer. Disable with E2E_CONTRACT=0.

type exchange struct {
	method, rawURL string
	reqBody        []byte
	reqHeader      map[string]string
	status         int
	respHeader     http.Header
	respBody       []byte
}

var (
	exMu      sync.Mutex
	exchanges []exchange
)

func record(method, rawURL string, headers map[string]string, body any, res *http.Response, raw []byte) {
	var rb []byte
	if body != nil {
		rb, _ = json.Marshal(body)
	}
	exMu.Lock()
	defer exMu.Unlock()
	exchanges = append(exchanges, exchange{method, rawURL, rb, headers, res.StatusCode, res.Header.Clone(), raw})
}

// specFor picks the OpenAPI file by which service answered and whether the path is internal.
func specFor(rawURL string) string {
	u, _ := url.Parse(rawURL)
	switch {
	case strings.HasPrefix(rawURL, queueURL):
		return "queue.yaml"
	case strings.HasPrefix(rawURL, paymentURL):
		return "payment.yaml"
	case strings.HasPrefix(rawURL, userURL):
		return "user.yaml"
	case strings.HasPrefix(rawURL, bookingURL) && strings.HasPrefix(u.Path, "/internal/"):
		return "internal-booking.yaml"
	case strings.HasPrefix(rawURL, bookingURL):
		return "booking.yaml"
	}
	return ""
}

func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 && os.Getenv("E2E_CONTRACT") != "0" {
		if bad := checkContracts(); bad > 0 {
			fmt.Fprintf(os.Stderr, "\ncontract: %d pelanggaran terhadap OpenAPI\n", bad)
			code = 1
		}
	}
	os.Exit(code)
}

func checkContracts() int {
	dir := env("E2E_OPENAPI_DIR", filepath.Join("..", "..", "docs", "contracts", "openapi"))
	validators := map[string]validator.Validator{}
	bad := 0
	seen := map[string]bool{}
	exMu.Lock()
	defer exMu.Unlock()
	for _, ex := range exchanges {
		file := specFor(ex.rawURL)
		if file == "" {
			continue
		}
		v, ok := validators[file]
		if !ok {
			raw, err := os.ReadFile(filepath.Join(dir, file))
			if err != nil {
				fmt.Fprintf(os.Stderr, "contract: %v\n", err)
				return 1
			}
			doc, err := libopenapi.NewDocument(raw)
			if err != nil {
				fmt.Fprintf(os.Stderr, "contract: %s: %v\n", file, err)
				return 1
			}
			v, errs := validator.NewValidator(doc)
			if len(errs) > 0 {
				fmt.Fprintf(os.Stderr, "contract: %s: %v\n", file, errs)
				return 1
			}
			validators[file] = v
		}
		v = validators[file]

		req, _ := http.NewRequest(ex.method, ex.rawURL, bytes.NewReader(ex.reqBody))
		if len(ex.reqBody) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, val := range ex.reqHeader {
			req.Header.Set(k, val)
		}
		resp := &http.Response{StatusCode: ex.status, Header: ex.respHeader, Body: io.NopCloser(bytes.NewReader(ex.respBody)), Request: req}
		resp.Header.Set("Content-Type", "application/json")
		if ok, errs := v.ValidateHttpResponse(req, resp); !ok {
			for _, e := range errs {
				key := fmt.Sprintf("%s %s %d %s", ex.method, req.URL.Path, ex.status, e.Message)
				if seen[key] {
					continue
				}
				seen[key] = true
				bad++
				fmt.Fprintf(os.Stderr, "contract: %s %s -> %d (%s): %s\n", ex.method, req.URL.Path, ex.status, file, e.Message)
				for _, r := range e.SchemaValidationErrors {
					fmt.Fprintf(os.Stderr, "    %s: %s\n", r.FieldPath, r.Reason)
				}
			}
		}
	}
	return bad
}
