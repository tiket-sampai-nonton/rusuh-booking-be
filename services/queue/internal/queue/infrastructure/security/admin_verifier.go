package security

import (
	"errors"
	"strings"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/revocation"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/token"
)

// ScopeBuyer is the scope of a User Service access token.
const ScopeBuyer = "buyer"

// AdminVerifier implements outbound.IAdminTokenVerifier (QSA-5, ADR-025): a User Service buyer-scope token whose email
// is in the allow-list. The semantics mirror Booking's admin verifier (entry "@domain" admits a whole domain; an empty
// list admits nobody) but this is a separate adapter because services never import each other.
//
// The scope check is mandatory here: Queue's own key also signs visitor, queue, admission and service tokens, and the
// composition root may fall back to that key for user tokens, so a valid signature alone proves nothing.
type AdminVerifier struct {
	verifier *token.Verifier
	now      func() time.Time
	admins   map[string]bool // lower-cased emails or "@domain" entries
	deny     revocation.Denylist
}

// errAdminDenied is the only error text produced: it carries no email, subject or bearer.
var errAdminDenied = errors.New("security: akses admin ditolak")

// NewAdminVerifier builds the verifier from the User Service key verifier and the allow-list.
func NewAdminVerifier(verifier *token.Verifier, now func() time.Time, emails []string) *AdminVerifier {
	set := map[string]bool{}
	for _, e := range emails {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			set[e] = true
		}
	}
	return &AdminVerifier{verifier: verifier, now: now, admins: set}
}

// WithDenylist refuses tokens whose session logged out, and every token when the denylist cannot be read.
func (v *AdminVerifier) WithDenylist(d revocation.Denylist) *AdminVerifier {
	v.deny = d
	return v
}

// VerifyAdmin returns the token subject (account id) for an allow-listed admin, else an error.
func (v *AdminVerifier) VerifyAdmin(bearer string) (string, error) {
	if v == nil || v.verifier == nil {
		return "", errAdminDenied
	}
	claims, err := v.verifier.Verify(bearer, v.now())
	if err != nil {
		return "", errAdminDenied
	}
	if claims.Scope != ScopeBuyer || claims.Subject == "" {
		return "", errAdminDenied
	}
	if err := revocation.Check(v.deny, claims); err != nil {
		return "", errAdminDenied
	}
	if !v.isAdmin(claims.Email) {
		return "", errAdminDenied
	}
	return claims.Subject, nil
}

func (v *AdminVerifier) isAdmin(email string) bool {
	email = strings.ToLower(email)
	if email == "" {
		return false
	}
	if v.admins[email] {
		return true
	}
	if at := strings.LastIndex(email, "@"); at > 0 {
		return v.admins[email[at:]]
	}
	return false
}
