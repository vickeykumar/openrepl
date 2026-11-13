package server

import (
	"testing"
	"time"

	"livepreview"
)

func TestTheTimeAPreviewLinkWorksIsCheckedAndDefaultsToTwoHours(t *testing.T) {
	for _, m := range []int{0, 10, 120, 1440} {
		if _, err := (SiteSettings{PreviewLinkMinutes: m}).normalize(); err != nil {
			t.Errorf("%d minutes is allowed: %v", m, err)
		}
	}
	for _, m := range []int{-1, 1, 9, 1441, 100000} {
		if _, err := (SiteSettings{PreviewLinkMinutes: m}).normalize(); err == nil {
			t.Errorf("%d minutes was taken", m)
		}
	}
	if got := previewTokenLife(SiteSettings{}); got != livepreview.TokenLife || got != 2*time.Hour {
		t.Errorf("nothing set: %v, want the built-in 2 hours", got)
	}
	if got := previewTokenLife(SiteSettings{PreviewLinkMinutes: 30}); got != 30*time.Minute {
		t.Errorf("30 minutes: %v", got)
	}
	if got := previewTokenLife(SiteSettings{PreviewLinkMinutes: 5000}); got != 2*time.Hour {
		t.Errorf("a number out of range must not be used: %v", got)
	}
}

func TestALinkMadeWithTheSettingEndsWhenTheSettingSays(t *testing.T) {
	life := previewTokenLife(SiteSettings{PreviewLinkMinutes: 45})
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
	has(SiteSettings{}, SiteSettings{PreviewLinkMinutes: 30}, "preview link works 30 minutes")
	has(SiteSettings{PreviewLinkMinutes: 30}, SiteSettings{}, "preview link time back to the built-in 2 hours")
}
