package nik_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/nik"
)

// now is a fixed reference date used for all "not in the future" checks.
var now = time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr error
	}{
		// --- valid ---
		{"laki-laki valid", "3175011506900001", nil},
		{"perempuan valid DD+40", "3175015506900001", nil},
		{"provinsi batas bawah 11", "1171011506900001", nil},
		{"provinsi batas atas 97", "9771011506900001", nil},
		{"tanggal 01 bulan 01", "3175010101900001", nil},
		{"tanggal 31 bulan 12", "3175013112900001", nil},
		{"nomor urut 9999", "3175011506909999", nil},

		// --- ErrLength ---
		{"terlalu pendek 15 digit", "317501150690000", nik.ErrLength},
		{"terlalu panjang 17 digit", "31750115069000011", nik.ErrLength},
		{"kosong", "", nik.ErrLength},

		// --- ErrDigits ---
		{"mengandung huruf", "317501150690000X", nik.ErrDigits},
		{"mengandung tanda", "3175-115069000 1", nik.ErrDigits},

		// --- ErrProvince ---
		{"provinsi 00", "0075011506900001", nik.ErrProvince},
		{"provinsi 20 tidak ada", "2075011506900001", nik.ErrProvince},
		{"provinsi 98 tidak ada", "9875011506900001", nik.ErrProvince},
		{"kab/kota 00", "3100011506900001", nik.ErrProvince},
		{"kecamatan 00", "3175001506900001", nik.ErrProvince},

		// --- ErrBirth ---
		{"30 Februari laki-laki", "3175013002900001", nik.ErrBirth},
		{"70-40=30 Februari perempuan", "3175017002900001", nik.ErrBirth},
		{"bulan 00", "3175010000900001", nik.ErrBirth},
		{"bulan 13", "3175011300900001", nik.ErrBirth},
		{"hari 00", "3175010001900001", nik.ErrBirth},
		{"hari 32", "3175013201900001", nik.ErrBirth},
		{"lahir di masa depan 22-10-2026 > now 21-10-2026", "3175012210260001", nik.ErrBirth},

		// --- ErrSerial ---
		{"nomor urut 0000", "3175011506900000", nik.ErrSerial},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := nik.Validate(tc.input, now)
			if tc.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.True(t, errors.Is(err, tc.wantErr),
					"got %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestValidate_DeterministicNow confirms that injecting different now values changes results.
func TestValidate_DeterministicNow(t *testing.T) {
	// NIK with birth date 15-06-2026 (yy=26 -> year=2026 since 2026 <= now.Year())
	nikStr := "3175011506260001"

	// One day before: 14 Jun 2026 -- still in the future -> ErrBirth
	assert.ErrorIs(t, nik.Validate(nikStr, time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)), nik.ErrBirth)

	// On that birthday: 15 Jun 2026 -- d.After(now) is false -> valid
	assert.NoError(t, nik.Validate(nikStr, time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)))

	// After: 16 Jun 2026 -- past -> valid
	assert.NoError(t, nik.Validate(nikStr, time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC)))
}

// TestValidate_CenturyRollover confirms the two-digit year disambiguation.
// yy=27 -> 2027 > now.Year() (2026) -> year becomes 1927.
func TestValidate_CenturyRollover(t *testing.T) {
	// Birth 15-06-1927 encoded as yy=27 -> valid (in the past)
	assert.NoError(t, nik.Validate("3175011506270001", now))
}

// TestValidate_AllValidProvinces iterates every province in the Kemendagri list.
func TestValidate_AllValidProvinces(t *testing.T) {
	validProvinces := []int{
		11, 12, 13, 14, 15, 16, 17, 18, 19,
		21,
		31, 32, 33, 34, 35, 36,
		51, 52, 53,
		61, 62, 63, 64, 65,
		71, 72, 73, 74, 75, 76,
		81, 82,
		91, 92, 93, 94, 95, 96, 97,
	}
	for _, p := range validProvinces {
		t.Run(fmt.Sprintf("provinsi_%02d", p), func(t *testing.T) {
			n := fmt.Sprintf("%02d75011506900001", p)
			assert.NoError(t, nik.Validate(n, now), "province %d should be valid", p)
		})
	}
}

// TestValidate_InvalidProvinces iterates known gaps in the Kemendagri list.
func TestValidate_InvalidProvinces(t *testing.T) {
	invalidProvinces := []int{10, 20, 22, 30, 37, 50, 54, 60, 66, 70, 77, 80, 83, 90, 98, 99}
	for _, p := range invalidProvinces {
		t.Run(fmt.Sprintf("provinsi_%02d_invalid", p), func(t *testing.T) {
			n := fmt.Sprintf("%02d75011506900001", p)
			assert.ErrorIs(t, nik.Validate(n, now), nik.ErrProvince, "province %d should be invalid", p)
		})
	}
}
