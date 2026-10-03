package utils

import "testing"

func TestGenieRatesDefaultToTheBuiltInOnes(t *testing.T) {
	t.Cleanup(func() { SetGenieRates(0, 0) })
	SetGenieRates(0, 0)
	if GuestFactor() != GUEST_FACTOR || UserFactor() != USER_FACTOR {
		t.Errorf("defaults = %v %v, want %v %v", GuestFactor(), UserFactor(), GUEST_FACTOR, USER_FACTOR)
	}
	SetGenieRates(2, 5)
	if GuestFactor() != 2 || UserFactor() != 5 {
		t.Errorf("rates = %v %v, want 2 5", GuestFactor(), UserFactor())
	}
	SetGenieRates(-1, 7) // a rate that is not positive restores that one's default
	if GuestFactor() != GUEST_FACTOR || UserFactor() != 7 {
		t.Errorf("rates = %v %v, want %v 7", GuestFactor(), UserFactor(), GUEST_FACTOR)
	}
}
