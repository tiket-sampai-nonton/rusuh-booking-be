package entity

import "errors"

// Repository sentinel errors (dipetakan usecase ke error domain).
var (
	ErrNotFound = errors.New("user: data tidak ditemukan")
	ErrNIKTaken = errors.New("user: nik_hash sudah dipakai akun lain")
	ErrRevoked  = errors.New("user: sesi sudah dicabut")
)
