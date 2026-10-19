package valueobject

import (
	"errors"
	"strconv"
)

// ErrInvalidNIK is returned when a NIK fails the structural check.
var ErrInvalidNIK = errors.New("NIK tidak valid")

// NIK is a 16-digit Indonesian national identity number that passed the structural check
// (kode provinsi, tanggal lahir dengan +40 untuk perempuan, bulan, nomor urut). Validasi ini tidak
// membuktikan identitas; tidak ada integrasi Dukcapil (PRD: di luar cakupan).
type NIK struct{ value string }

// NewNIK validates s.
func NewNIK(s string) (NIK, error) {
	if len(s) != 16 {
		return NIK{}, ErrInvalidNIK
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return NIK{}, ErrInvalidNIK
		}
	}
	province, _ := strconv.Atoi(s[0:2])
	day, _ := strconv.Atoi(s[6:8])
	month, _ := strconv.Atoi(s[8:10])
	serial, _ := strconv.Atoi(s[12:16])
	if province < 11 || province > 94 {
		return NIK{}, ErrInvalidNIK
	}
	if day > 40 {
		day -= 40
	}
	if day < 1 || day > 31 || month < 1 || month > 12 || serial == 0 {
		return NIK{}, ErrInvalidNIK
	}
	return NIK{value: s}, nil
}

// String returns the digits.
func (n NIK) String() string { return n.value }
