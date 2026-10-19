package valueobject

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewEmail(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"  Arnu@Gmail.COM ", "arnu@gmail.com", true},
		{"a.b+c@sub.domain.id", "a.b+c@sub.domain.id", true},
		{"no-at.example.com", "", false},
		{"a@b", "", false},
		{"a b@c.id", "", false},
		{"a@-bad.id", "", false},
		{strings.Repeat("a", 65) + "@x.id", "", false},
		{"a@" + strings.Repeat("b", 250) + ".id", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			e, err := NewEmail(tt.in)
			if !tt.ok {
				assert.ErrorIs(t, err, ErrInvalidEmail)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, e.String())
		})
	}
}

func TestNewNIK(t *testing.T) {
	tests := []struct {
		in string
		ok bool
	}{
		{"3174010101900001", true},  // laki-laki, 01-01-1990
		{"3174014101900002", true},  // perempuan: tanggal 41 → 01
		{"317401010190000", false},  // 15 digit
		{"31740101019000a1", false}, // huruf
		{"0974010101900001", false}, // provinsi < 11
		{"9974010101900001", false}, // provinsi > 94
		{"3174013201900001", false}, // tanggal 32
		{"3174017201900001", false}, // tanggal 72 (32 perempuan)
		{"3174010113900001", false}, // bulan 13
		{"3174010100900001", false}, // bulan 00
		{"3174010101900000", false}, // nomor urut 0000
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			n, err := NewNIK(tt.in)
			if !tt.ok {
				assert.ErrorIs(t, err, ErrInvalidNIK)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.in, n.String())
		})
	}
}

func TestNewOTPCode(t *testing.T) {
	c, err := NewOTPCode("012345")
	assert.NoError(t, err)
	assert.Equal(t, "012345", c.String())
	for _, bad := range []string{"", "12345", "1234567", "12a456"} {
		_, err := NewOTPCode(bad)
		assert.ErrorIs(t, err, ErrInvalidOTPCode, bad)
	}
}
