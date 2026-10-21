package usecase

import "regexp"

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// isUUID reports whether s is a canonical UUID string.
func isUUID(s string) bool { return uuidRe.MatchString(s) }
