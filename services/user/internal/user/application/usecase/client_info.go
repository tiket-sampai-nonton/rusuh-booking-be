package usecase

import (
	"net"
	"strings"
	"unicode"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/inbound"
)

// maxUserAgentRunes batasi panjang user agent yang disimpan.
const maxUserAgentRunes = 256

// normalizeClient makes client data safe for storage and keys: IP must parse (else "" so Postgres inet never gets
// garbage and the audit column becomes NULL), the user agent must be valid UTF-8 without control characters.
func normalizeClient(c inbound.ClientInfo) inbound.ClientInfo {
	ip := ""
	if parsed := net.ParseIP(strings.TrimSpace(c.IP)); parsed != nil {
		ip = parsed.String()
	}
	return inbound.ClientInfo{IP: ip, UserAgent: sanitizeUA(c.UserAgent, maxUserAgentRunes)}
}

// sanitizeUA drops invalid UTF-8 and control runes (termasuk NUL, yang ditolak Postgres), lalu memotong per rune.
func sanitizeUA(s string, maxRunes int) string {
	s = strings.ToValidUTF8(s, "")
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		if len(out) == maxRunes {
			break
		}
		out = append(out, r)
	}
	return string(out)
}

// ipBucket is the rate-limit identity of a client address: IPv4 as is, IPv6 grouped by /64 (satu pelanggan
// biasanya mendapat satu /64, jadi rotasi alamat di dalamnya tidak melewati batas), unknown otherwise.
func ipBucket(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "unknown"
	}
	if v4 := parsed.To4(); v4 != nil {
		return v4.String()
	}
	return parsed.Mask(net.CIDRMask(64, 128)).String() + "/64"
}
