// Package nik memvalidasi struktur NIK (Nomor Induk Kependudukan) secara internal.
// Validasi struktur tidak membuktikan bahwa NIK terdaftar atau dimiliki pengguna.
//
// Pemakaian di usecase (bukan di domain):
//
//	if err := nik.Validate(req.NIK, clock.Now()); err != nil {
//	    return inbound.Validation("nik", err)
//	}
//
// Domain value object tetap memeriksa panjang/digit sebagai pertahanan kedua; daftar provinsi
// penuh hanya ada di sini untuk menghindari duplikasi antar service (SDD-RBT §4.9, platform R1).
package nik

import (
	"errors"
	"time"
)

// Sentinel errors — service memetakan semuanya ke kode error NIK_INVALID.
var (
	ErrLength   = errors.New("NIK_INVALID_LENGTH")
	ErrDigits   = errors.New("NIK_INVALID_CHARACTERS")
	ErrProvince = errors.New("NIK_INVALID_REGION")
	ErrBirth    = errors.New("NIK_INVALID_BIRTH_DATE")
	ErrSerial   = errors.New("NIK_INVALID_SERIAL")
)

// provinces adalah daftar kode provinsi yang berlaku (Kemendagri).
// Dikelola sebagai data, bukan logika bercabang (platform requirement 1.3).
var provinces = map[int]bool{
	11: true, 12: true, 13: true, 14: true, 15: true, 16: true, 17: true, 18: true, 19: true,
	21: true,
	31: true, 32: true, 33: true, 34: true, 35: true, 36: true,
	51: true, 52: true, 53: true,
	61: true, 62: true, 63: true, 64: true, 65: true,
	71: true, 72: true, 73: true, 74: true, 75: true, 76: true,
	81: true, 82: true,
	91: true, 92: true, 93: true, 94: true, 95: true, 96: true, 97: true,
}

// num parses a pure-digit substring into an integer. Caller guarantees s contains only '0'-'9'.
func num(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// Validate memeriksa struktur NIK; tidak membuktikan NIK terdaftar (tanpa Dukcapil).
//
// Aturan (SDD-RBT §4.9, platform R1):
//   - Tepat 16 digit numerik.
//   - Kode provinsi [0:2] ada di daftar Kemendagri.
//   - Kode kab/kota [2:4] dan kecamatan [4:6] bukan 00.
//   - Tanggal lahir DDMMYY valid secara kalender (DD+40 untuk perempuan) dan tidak di masa depan.
//   - Nomor urut [12:16] bukan 0000.
//
// now di-inject (pakai pkg/clock) agar uji tanggal di masa depan deterministik.
// NIK tidak pernah ditulis ke log atau pesan error (platform requirement 1.4).
func Validate(s string, now time.Time) error {
	if len(s) != 16 {
		return ErrLength
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return ErrDigits
		}
	}

	if !provinces[num(s[0:2])] || num(s[2:4]) == 0 || num(s[4:6]) == 0 {
		return ErrProvince
	}

	day, month, yy := num(s[6:8]), num(s[8:10]), num(s[10:12])
	if day > 40 {
		day -= 40 // perempuan: DD+40
	}
	year := 2000 + yy
	if year > now.Year() {
		year -= 100 // dua digit: pilih abad sebelumnya bila tahun masih di masa depan
	}
	// time.Date normalises invalid dates (e.g. Feb 30 -> Mar 2); d.Day() != day catches them.
	d := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if month < 1 || month > 12 || day < 1 || d.Day() != day || d.After(now) {
		return ErrBirth
	}

	if num(s[12:16]) == 0 {
		return ErrSerial
	}
	return nil
}
