package entity

import "testing"

func TestMapValidationErrors(t *testing.T) {
	errs := ValidationErrors{{Field: "nik", Message: "harus 16 digit"}, {Field: "email", Message: "wajib"}}

	got := MapValidationErrors(errs)

	if got["nik"] != "harus 16 digit" || got["email"] != "wajib" || errs.Error() == "" {
		t.Fatalf("got %v / %q", got, errs.Error())
	}
}
