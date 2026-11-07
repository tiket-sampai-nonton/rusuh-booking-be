package usecase

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/inbound"
)

func TestSanitizeUA(t *testing.T) {
	multibyte := strings.Repeat("日", 300) // 900 byte
	tests := []struct {
		name, in string
		wantRune int
	}{
		{"multibyte cut at runes", multibyte, 256},
		{"invalid utf8 dropped", "ab\xffcd", 4},
		{"nul dropped", "ab\x00cd", 4},
		{"control dropped", "a\x07\x1fb", 2},
		{"256 ascii unchanged", strings.Repeat("a", 256), 256},
		{"257 ascii cut", strings.Repeat("a", 257), 256},
		{"empty", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeUA(tt.in, 256)
			assert.True(t, utf8.ValidString(got))
			assert.Equal(t, tt.wantRune, utf8.RuneCountInString(got))
			assert.NotContains(t, got, "\x00")
		})
	}
	assert.Equal(t, strings.Repeat("a", 256), sanitizeUA(strings.Repeat("a", 256), 256))
}

func TestNormalizeClient(t *testing.T) {
	tests := []struct {
		name string
		in   inbound.ClientInfo
		want inbound.ClientInfo
	}{
		{"canonical ipv4", inbound.ClientInfo{IP: "203.0.113.9", UserAgent: "x"}, inbound.ClientInfo{IP: "203.0.113.9", UserAgent: "x"}},
		{"ipv6 canonical", inbound.ClientInfo{IP: "2001:DB8::1"}, inbound.ClientInfo{IP: "2001:db8::1"}},
		{"garbage ip", inbound.ClientInfo{IP: "not-an-ip"}, inbound.ClientInfo{IP: ""}},
		{"empty ip", inbound.ClientInfo{}, inbound.ClientInfo{}},
		{"bad ua", inbound.ClientInfo{IP: "1.1.1.1", UserAgent: "a\xff\x00b"}, inbound.ClientInfo{IP: "1.1.1.1", UserAgent: "ab"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assert.Equal(t, tt.want, normalizeClient(tt.in)) })
	}
}

func TestIPBucket(t *testing.T) {
	assert.Equal(t, "203.0.113.9", ipBucket("203.0.113.9"))
	assert.Equal(t, ipBucket("2001:db8:1:2::1"), ipBucket("2001:db8:1:2:ffff:ffff:ffff:ffff"), "satu /64 berbagi bucket")
	assert.NotEqual(t, ipBucket("2001:db8:1:2::1"), ipBucket("2001:db8:1:3::1"))
	assert.Equal(t, "203.0.113.9", ipBucket("::ffff:203.0.113.9"), "IPv4-mapped menjadi IPv4")
	assert.Equal(t, "unknown", ipBucket(""))
	assert.Equal(t, "unknown", ipBucket("garbage"))
}
