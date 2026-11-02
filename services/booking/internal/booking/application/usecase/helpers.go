package usecase

import "regexp"

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// isUUID reports whether s is a canonical UUID string.
// errEventIDNotUUID is the validation message shared by every usecase that takes an event id.
const errEventIDNotUUID = "eventId harus UUID"

func isUUID(s string) bool { return uuidRe.MatchString(s) }
