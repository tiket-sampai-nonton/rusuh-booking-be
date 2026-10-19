package usecase

import "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"

func maskEmail(email string) string { return crypto.MaskEmail(email) }

func maskNIK(nik string) string { return crypto.MaskNIK(nik) }
