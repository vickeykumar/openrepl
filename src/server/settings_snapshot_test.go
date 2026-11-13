package server

import (
	"testing"
	"time"
)

func TestTheTimeBetweenWorkspaceSnapshotsIsCheckedAndDefaultsToFiveMinutes(t *testing.T) {
	for _, m := range []int{0, 1, 5, 60} {
		if _, err := (SiteSettings{SnapshotIntervalMinutes: m}).normalize(); err != nil {
			t.Errorf("%d minutes is allowed: %v", m, err)
		}
	}
	for _, m := range []int{-1, 61, 1000} {
		if _, err := (SiteSettings{SnapshotIntervalMinutes: m}).normalize(); err == nil {
			t.Errorf("%d minutes was taken", m)
		}
	}
	if d := (SiteSettings{}).snapshotInterval(); d != 0 {
		t.Errorf("nothing set: %v (0 means the built-in time)", d)
	}
	if d := (SiteSettings{SnapshotIntervalMinutes: 2}).snapshotInterval(); d != 2*time.Minute {
		t.Errorf("2 minutes: %v", d)
	}
	if d := (SiteSettings{SnapshotIntervalMinutes: 500}).snapshotInterval(); d != 0 {
		t.Errorf("a number out of range must not be used: %v", d)
	}
}
