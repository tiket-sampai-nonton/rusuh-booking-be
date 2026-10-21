package entity

import "errors"

// Domain sentinel errors.
var (
	// ErrNotFound is returned when a row (subscription/delivery) does not exist.
	ErrNotFound = errors.New("notification: tidak ditemukan")
	// ErrAlreadyProcessed is returned when a delivery was already claimed (duplicate event).
	ErrAlreadyProcessed = errors.New("notification: sudah diproses")
)
