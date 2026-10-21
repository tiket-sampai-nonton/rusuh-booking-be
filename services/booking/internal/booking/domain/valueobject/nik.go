package valueobject

import (
	"strconv"
	"strings"
)

// NIKProtector hashes and seals NIK values. Implemented by the security adapter over pkg/crypto.
type NIKProtector interface {
	HashNIK(nik string) []byte
	SealNIK(nik string) ([]byte, error)
}

// NIK is a 16-digit Indonesian national identity number that passed the structural check (Lampiran A.1).
// The plaintext is kept private: String() and MarshalJSON never reveal it, so a NIK can be logged or
// serialised without leaking PII. Persisted forms are the HMAC hash, the AES-256-GCM ciphertext, and
// the masked string.
type NIK struct {
	hash   []byte
	masked string
	enc    []byte
}

// ParseNIK validates raw against Lampiran A.1 and derives hash/masked/enc via the protector.
// A structurally invalid NIK returns ErrNIKInvalid at the domain layer (via the caller mapping).
func ParseNIK(raw string, p NIKProtector) (NIK, error) {
	if !ValidNIKStructure(raw) {
		return NIK{}, ErrInvalidNIK
	}
	enc, err := p.SealNIK(raw)
	if err != nil {
		return NIK{}, err
	}
	return NIK{hash: p.HashNIK(raw), masked: maskNIK(raw), enc: enc}, nil
}

// ParseNIKHashOnly validates structure and computes only the lookup hash + mask (no encryption).
// Used by attendee-checks, where the plaintext is never stored, only probed.
func ParseNIKHashOnly(raw string, p NIKProtector) (NIK, error) {
	if !ValidNIKStructure(raw) {
		return NIK{}, ErrInvalidNIK
	}
	return NIK{hash: p.HashNIK(raw), masked: maskNIK(raw)}, nil
}

// Hash returns the HMAC-SHA256 lookup hash (buyer_nik_hash / attendee_nik_hash bytea column).
func (n NIK) Hash() []byte { return n.hash }

// Masked returns the display form, e.g. "3174********0001".
func (n NIK) Masked() string { return n.masked }

// Enc returns the AES-256-GCM ciphertext (attendee_nik_enc), or nil when parsed hash-only.
func (n NIK) Enc() []byte { return n.enc }

// String never reveals the plaintext.
func (n NIK) String() string { return n.masked }

// MarshalJSON emits only the masked form so a NIK can never be serialised to plaintext.
func (n NIK) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(n.masked)), nil
}

// ErrInvalidNIK is the sentinel returned when a NIK fails the structural check; the usecase maps it to
// the domain.ErrNIKInvalid (422 NIK_INVALID).
var ErrInvalidNIK = errNIK("NIK tidak valid")

type errNIK string

func (e errNIK) Error() string { return string(e) }

// ValidNIKStructure enforces Lampiran A.1: exactly 16 digits, province [0:2] in 01..99, birth day [6:8]
// <= 31 (male) or 41..71 (female, +40). It performs no I/O and is safe to call on untrusted input.
func ValidNIKStructure(raw string) bool {
	raw = strings.TrimSpace(raw)
	if len(raw) != 16 {
		return false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return false
		}
	}
	province, _ := strconv.Atoi(raw[0:2])
	if province < 1 || province > 99 {
		return false
	}
	day, _ := strconv.Atoi(raw[6:8])
	if day > 40 {
		day -= 40 // female
	}
	return day >= 1 && day <= 31
}

// maskNIK returns e.g. "3174********0001" for a 16-digit NIK (stdlib only — domain is framework-free).
func maskNIK(nik string) string {
	if len(nik) != 16 {
		return strings.Repeat("*", 16)
	}
	return nik[:4] + strings.Repeat("*", 8) + nik[12:]
}
