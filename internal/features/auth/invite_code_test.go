package auth

import "testing"

// TestNormalizeInviteCodeAcceptsWhatPeopleType pins the accept side of the
// staff code: whatever an employee squints at and types must reduce to the
// exact base32 string the team screen minted, or their only way in fails.
func TestNormalizeInviteCodeAcceptsWhatPeopleType(t *testing.T) {
	const minted = "K7QR4XZP2M5T"
	for _, typed := range []string{
		minted,
		"k7qr4xzp2m5t",
		"K7QR-4XZP-2M5T",
		"  k7qr 4XZP-2m5t  ",
	} {
		if got := normalizeInviteCode(typed); got != minted {
			t.Errorf("normalizing %q gave %q, want %q", typed, got, minted)
		}
	}
}
