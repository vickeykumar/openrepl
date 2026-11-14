package server

import "testing"

func TestShareLinkLimits(t *testing.T) {
	var s SiteSettings
	if s.shareLinkMax(true) != 60 || s.shareLinkMax(false) != 10080 {
		t.Errorf("defaults: %d %d", s.shareLinkMax(true), s.shareLinkMax(false))
	}
	s.ShareLinkGuestMinutes, s.ShareLinkUserMinutes = 30, 720
	if s.shareLinkMax(true) != 30 || s.shareLinkMax(false) != 720 {
		t.Errorf("set: %d %d", s.shareLinkMax(true), s.shareLinkMax(false))
	}
	pub := s.public()
	if pub.ShareMaxGuestMinutes != 30 || pub.ShareMaxUserMinutes != 720 {
		t.Errorf("public: %d %d", pub.ShareMaxGuestMinutes, pub.ShareMaxUserMinutes)
	}
	for _, bad := range []int{5, 10081, -3} {
		if _, err := (SiteSettings{ShareLinkGuestMinutes: bad}).normalize(); err == nil {
			t.Errorf("%d minutes was accepted", bad)
		}
	}
	if _, err := (SiteSettings{ShareLinkUserMinutes: 10080}).normalize(); err != nil {
		t.Errorf("7 days was refused: %v", err)
	}
}
