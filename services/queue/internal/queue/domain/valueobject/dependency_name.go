// Package valueobject contains immutable domain values.
package valueobject

import (
	"errors"
	"regexp"
)

// ErrInvalidDependencyName is returned for names that are not lowercase identifiers.
var ErrInvalidDependencyName = errors.New("nama dependency harus huruf kecil, angka, atau tanda hubung")

var dependencyNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// DependencyName identifies an infrastructure dependency (postgres, valkey, rabbitmq, ...).
type DependencyName struct{ value string }

// NewDependencyName validates and creates a DependencyName.
func NewDependencyName(s string) (DependencyName, error) {
	if !dependencyNamePattern.MatchString(s) {
		return DependencyName{}, ErrInvalidDependencyName
	}
	return DependencyName{value: s}, nil
}

// String returns the name.
func (n DependencyName) String() string { return n.value }
