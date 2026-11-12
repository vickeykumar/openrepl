package server

import (
	"strings"
	"testing"
)

// The largest file the editor opens is an admin's setting: 1 to 16 MB (16 is all one Firebase write can carry), empty
// meaning 16, and the page is told the number in force.
func TestTheLargestEditorFileIsBetweenOneAndSixteenMegabytes(t *testing.T) {
	for _, mb := range []int{1, 4, 16} {
		s, err := (SiteSettings{MaxEditorFileMB: mb}).normalize()
		if err != nil || s.MaxEditorFileMB != mb {
			t.Errorf("%d MB: %+v %v", mb, s.MaxEditorFileMB, err)
		}
	}
	if s, err := (SiteSettings{}).normalize(); err != nil || s.MaxEditorFileMB != 0 {
		t.Errorf("empty: %+v %v", s.MaxEditorFileMB, err)
	}
	for _, mb := range []int{-1, 17, 100, 1000000} {
		if _, err := (SiteSettings{MaxEditorFileMB: mb}).normalize(); err == nil {
			t.Errorf("%d MB was accepted", mb)
		}
	}
}

func TestThePageIsToldTheLimitInForce(t *testing.T) {
	if got := (SiteSettings{}).public().MaxEditorFileMB; got != 16 {
		t.Errorf("none set: %d, want 16", got)
	}
	if got := (SiteSettings{MaxEditorFileMB: 4}).public().MaxEditorFileMB; got != 4 {
		t.Errorf("4 set: %d", got)
	}
	// a value that got into the file by hand, out of range, does not widen the limit past what Firebase takes
	if got := (SiteSettings{MaxEditorFileMB: 500}).public().MaxEditorFileMB; got != 16 {
		t.Errorf("500 in the file: %d, want 16", got)
	}
}

func TestAChangedLimitIsInTheAuditLog(t *testing.T) {
	changes := settingsChanges(SiteSettings{}, SiteSettings{MaxEditorFileMB: 4})
	if !strings.Contains(strings.Join(changes, "|"), "largest editor file 4 MB") {
		t.Errorf("changes: %v", changes)
	}
	if got := settingsChanges(SiteSettings{}, SiteSettings{MaxEditorFileMB: 16}); len(got) != 0 {
		t.Errorf("16 is the same as none: %v", got)
	}
}
