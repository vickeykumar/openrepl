package server

import (
	"testing"
	"time"

	"livepreview"
)

func TestTheTimeAPreviewLinkWorksIsCheckedAndDefaultsToAnHourForGuestsAndThreeForSignedInUsers(t *testing.T) {
	for _, m := range []int{0, 10, 120, 1440} {
		if _, err := (SiteSettings{PreviewLinkGuestMinutes: m, PreviewLinkUserMinutes: m}).normalize(); err != nil {
			t.Errorf("%d minutes is allowed: %v", m, err)
		}
	}
	for _, m := range []int{-1, 1, 9, 1441, 100000} {
		if _, err := (SiteSettings{PreviewLinkGuestMinutes: m}).normalize(); err == nil {
			t.Errorf("%d minutes was taken for guests", m)
		}
		if _, err := (SiteSettings{PreviewLinkUserMinutes: m}).normalize(); err == nil {
			t.Errorf("%d minutes was taken for signed-in users", m)
		}
	}
	if got := previewTokenLife(SiteSettings{}, true); got != time.Hour {
		t.Errorf("a guest, nothing set: %v, want 1 hour", got)
	}
	if got := previewTokenLife(SiteSettings{}, false); got != 3*time.Hour {
		t.Errorf("a signed-in user, nothing set: %v, want 3 hours", got)
	}
	s := SiteSettings{PreviewLinkGuestMinutes: 30, PreviewLinkUserMinutes: 90}
	if previewTokenLife(s, true) != 30*time.Minute || previewTokenLife(s, false) != 90*time.Minute {
		t.Errorf("each has its own: %v %v", previewTokenLife(s, true), previewTokenLife(s, false))
	}
	if got := previewTokenLife(SiteSettings{PreviewLinkGuestMinutes: 5000, PreviewLinkUserMinutes: 5}, false); got != 3*time.Hour {
		t.Errorf("a number out of range must not be used: %v", got)
	}
}

func TestALinkMadeWithTheSettingEndsWhenTheSettingSays(t *testing.T) {
	life := previewTokenLife(SiteSettings{PreviewLinkGuestMinutes: 45}, true)
	now := time.Now()
	tok, err := livepreview.NewViewerToken("secret", "/home/openrepl/ws", "v", now, life)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := livepreview.Home("secret", tok, now.Add(44*time.Minute)); !ok {
		t.Error("the link should still work at 44 minutes")
	}
	if _, ok := livepreview.Home("secret", tok, now.Add(46*time.Minute)); ok {
		t.Error("the link should have ended at 46 minutes")
	}
}

func TestTheChangeLogSaysWhenThePreviewLinkTimeChanges(t *testing.T) {
	has := func(a, b SiteSettings, want string) {
		t.Helper()
		for _, c := range settingsChanges(a, b) {
			if c == want {
				return
			}
		}
		t.Errorf("settingsChanges lacks %q: %v", want, settingsChanges(a, b))
	}
	has(SiteSettings{}, SiteSettings{PreviewLinkGuestMinutes: 30}, "preview link works 30 minutes for guests")
	has(SiteSettings{}, SiteSettings{PreviewLinkUserMinutes: 360}, "preview link works 360 minutes for signed-in users")
	has(SiteSettings{PreviewLinkGuestMinutes: 30}, SiteSettings{}, "preview link works 60 minutes for guests")
}

func TestTheAgentsAutoLevelsCanBeSwitchedOffAndTheChangeLogSaysSo(t *testing.T) {
	pub := (SiteSettings{Genie: GenieSettings{AgentAutoOff: true}}).public()
	if !pub.AgentAutoOff || pub.AgentRiskyOff {
		t.Errorf("the page is told: auto off %v, risky off %v", pub.AgentAutoOff, pub.AgentRiskyOff)
	}
	if (SiteSettings{}).public().AgentAutoOff || (SiteSettings{}).public().AgentRiskyOff {
		t.Error("both levels are available by default")
	}
	has := func(a, b SiteSettings, want string) {
		t.Helper()
		for _, c := range settingsChanges(a, b) {
			if c == want {
				return
			}
		}
		t.Errorf("settingsChanges lacks %q: %v", want, settingsChanges(a, b))
	}
	has(SiteSettings{}, SiteSettings{Genie: GenieSettings{AgentAutoOff: true}}, "agent: allow everything for a session off")
	has(SiteSettings{}, SiteSettings{Genie: GenieSettings{AgentRiskyOff: true}}, "agent: allowing riskier things for a while off")
}
