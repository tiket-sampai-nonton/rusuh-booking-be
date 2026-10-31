package valueobject

// Method is a payment method offered on the payment page (payment.yaml CreateAttemptRequest.method).
type Method string

// Payment methods.
const (
	MethodVABCA     Method = "VA_BCA"
	MethodVABNI     Method = "VA_BNI"
	MethodVABRI     Method = "VA_BRI"
	MethodVAPermata Method = "VA_PERMATA"
	MethodQRIS      Method = "QRIS"
	MethodGoPay     Method = "GOPAY"
	// MethodSnap is Midtrans SNAP: one backend-created token; the payer picks the channel inside the Snap page.
	MethodSnap Method = "SNAP"
)

// ParseMethod returns the method for s, or false when s is not a known method.
func ParseMethod(s string) (Method, bool) {
	switch m := Method(s); m {
	case MethodVABCA, MethodVABNI, MethodVABRI, MethodVAPermata, MethodQRIS, MethodGoPay, MethodSnap:
		return m, true
	}
	return "", false
}

// IsVirtualAccount reports whether the method pays to a bank virtual account.
func (m Method) IsVirtualAccount() bool {
	return m == MethodVABCA || m == MethodVABNI || m == MethodVABRI || m == MethodVAPermata
}
