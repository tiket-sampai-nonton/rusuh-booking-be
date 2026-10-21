package valueobject

import "errors"

// ErrNegativeMoney is returned when a money amount is below zero.
var ErrNegativeMoney = errors.New("money: jumlah tidak boleh negatif")

// Money is an amount in the minor unit of IDR (rupiah has no sub-unit in practice, so the minor unit
// equals one rupiah). It is always non-negative (DB CHECK amount_minor >= 0).
type Money struct{ minor int64 }

// NewMoney validates and wraps a minor-unit amount.
func NewMoney(minor int64) (Money, error) {
	if minor < 0 {
		return Money{}, ErrNegativeMoney
	}
	return Money{minor: minor}, nil
}

// Minor returns the raw minor-unit amount.
func (m Money) Minor() int64 { return m.minor }

// Add returns the sum of two amounts.
func (m Money) Add(other Money) Money { return Money{minor: m.minor + other.minor} }

// Mul multiplies the amount by a non-negative quantity.
func (m Money) Mul(qty int) Money {
	if qty < 0 {
		return Money{}
	}
	return Money{minor: m.minor * int64(qty)}
}

// Currency is the ISO-4217 code; the platform only trades in IDR.
const Currency = "IDR"
