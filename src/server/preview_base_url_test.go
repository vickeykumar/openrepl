package server

import "testing"

func TestThePreviewBaseURLIsAnOriginOrEmpty(t *testing.T) {
	for in, want := range map[string]string{"": "", "  ": "", "preview.example.test": "https://preview.example.test", "Preview.Example.test/": "https://preview.example.test", "localhost:9000": "https://localhost:9000", "https://preview.example.test": "https://preview.example.test", "https://preview.example.test/": "https://preview.example.test", "http://localhost:9000": "http://localhost:9000"} {
		got, err := cleanPreviewBaseURL(in)
		if err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"preview.example.test/path", "ftp://x.test", "https://x.test/path", "https://x.test/?q=1", "https://u:p@x.test", "https://x.test/#f", "//x.test"} {
		if got, err := cleanPreviewBaseURL(bad); err == nil {
			t.Errorf("%q must be refused, got %q", bad, got)
		}
	}
	if _, err := (SiteSettings{PreviewBaseURL: "https://x.test/path"}).normalize(); err == nil {
		t.Error("settings with a path in the preview base URL must not normalize")
	}
	// it reaches the page (settings.js) and the workers, and a change is told in the log of changes
	s := SiteSettings{PreviewBaseURL: "https://preview.example.test"}
	if s.public().PreviewBaseURL != "https://preview.example.test" {
		t.Error("the page does not get it")
	}
	if ch := settingsChanges(SiteSettings{}, s); len(ch) == 0 {
		t.Error("the change is not told")
	}
}
