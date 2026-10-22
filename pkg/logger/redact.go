package logger

import (
	"log/slog"
	"strings"
)

const redacted = "[REDACTED]"

// exactKeys adalah kunci sensitif R4.4 yang dicocokkan persis (setelah normalisasi). "code" hanya
// persis karena akhiran "code" juga mengenai errorCode/statusCode yang aman dilog.
var exactKeys = map[string]bool{
	"nik": true, "buyernik": true, "email": true, "emailconfirmation": true, "token": true,
	"authorization": true, "signature": true, "signaturekey": true, "password": true,
	"code": true, "pushtoken": true, "secret": true,
	// Tambahan di luar daftar R4.4: kredensial dan kontak yang lazim dilog tanpa sengaja.
	"otp": true, "otpcode": true, "apikey": true, "serverkey": true, "sessionid": true, "cookie": true, "phone": true,
}

// suffixKeys menangkap varian bernama (accessToken, buyerEmail, client_secret, userNik).
var suffixKeys = []string{"token", "secret", "password", "email", "nik"}

var keyNormalizer = strings.NewReplacer("_", "", "-", "")

// sensitive melaporkan apakah kunci log berisi data yang tidak boleh ditulis. Huruf besar/kecil,
// "_" dan "-" diabaikan sehingga buyer_nik, buyerNik, dan BUYER-NIK dianggap sama.
func sensitive(key string) bool {
	k := keyNormalizer.Replace(strings.ToLower(key))
	if exactKeys[k] {
		return true
	}
	for _, s := range suffixKeys {
		if strings.HasSuffix(k, s) {
			return true
		}
	}
	return false
}

// replaceAttr dipasang sebagai slog.HandlerOptions.ReplaceAttr. slog memanggilnya untuk setiap atribut
// daun (juga yang ada di dalam slog.Group, WithGroup, dan With), jadi satu aturan berlaku di semua handler.
func replaceAttr(_ []string, a slog.Attr) slog.Attr {
	if sensitive(a.Key) {
		return slog.String(a.Key, redacted)
	}
	if a.Value.Kind() == slog.KindAny {
		a.Value = slog.AnyValue(Redact(a.Value.Any()))
	}
	return a
}

// Redact mengembalikan salinan v dengan nilai kunci sensitif diganti "[REDACTED]". Hanya menelusuri
// map[string]any, map[string]string, map[string][]string (http.Header), []map[string]any, dan []any (bentuk hasil decode JSON, mis. payload request yang dilog
// saat error); tipe lain dikembalikan apa adanya, jadi jangan melog struct request mentah.
func Redact(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if sensitive(k) {
				out[k] = redacted
			} else {
				out[k] = Redact(val)
			}
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(x))
		for k, val := range x {
			if sensitive(k) {
				out[k] = redacted
			} else {
				out[k] = val
			}
		}
		return out
	case map[string][]string: // http.Header
		out := make(map[string][]string, len(x))
		for k, val := range x {
			if sensitive(k) {
				out[k] = []string{redacted}
			} else {
				out[k] = val
			}
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(x))
		for i, val := range x {
			out[i] = Redact(val).(map[string]any)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = Redact(val)
		}
		return out
	default:
		return v
	}
}
