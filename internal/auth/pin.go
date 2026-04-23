// Package auth handles PIN-based authentication for BitBangProxy.
//
// PIN verification happens over the DTLS-encrypted data channel, so the
// signaling server never sees the PIN.
package auth

// PINAuth manages PIN verification.
type PINAuth struct {
	pin string
}

// New creates a PINAuth with the given PIN.
// Returns nil if pin is empty (no auth required).
func New(pin string) *PINAuth {
	if pin == "" {
		return nil
	}
	return &PINAuth{pin: pin}
}

// Required returns true if PIN authentication is configured.
func (a *PINAuth) Required() bool {
	return a != nil && a.pin != ""
}

// Verify checks the PIN. Returns true if correct.
func (a *PINAuth) Verify(attempt string) bool {
	return attempt == a.pin
}
