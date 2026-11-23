package server

import "testing"

func TestThePreviewBaseURLIsKeptAsTypedAndTheOriginIsDerived(t *testing.T) {
	// the setting keeps what the admin typed (trimmed, lower case); the origin adds the scheme: http for a local host, else https
	for in, want := range map[string][2]string{
		"": {"", ""}, "  ": {"", ""},
		"preview.example.com":          {"preview.example.com", "https://preview.example.com"},
		"Preview.Example.com/":         {"preview.example.com", "https://preview.example.com"},
		"localhost:9000":               {"localhost:9000", "http://localhost:9000"},
		"preview.localhost:8080":       {"preview.localhost:8080", "http://preview.localhost:8080"},
		"https://preview.example.test": {"https://preview.example.test", "https://preview.example.test"},
		"http://preview.example.test/": {"http://preview.example.test", "http://preview.example.test"},
		"http://localhost:9000":        {"http://localhost:9000", "http://localhost:9000"},
	} {
		got, err := cleanPreviewBaseURL(in)
		if err != nil || got != want[0] {
			t.Errorf("%q: kept as %q %v, want %q", in, got, err, want[0])
		}
		if o := (SiteSettings{PreviewBaseURL: in}).previewBaseURL(); o != want[1] {
			t.Errorf("%q: origin %q, want %q", in, o, want[1])
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
	// the host name must differ from the site's own (OPENREPL_PUBLIC_URL), whatever the scheme, port or spelling of either
	for _, site := range []string{"openrepl.com", "http://openrepl.com", "https://openrepl.com", "https://OpenREPL.com:8443"} {
		t.Setenv("OPENREPL_PUBLIC_URL", site)
		for _, base := range []string{"openrepl.com", "http://openrepl.com", "https://openrepl.com:9000"} {
			if _, err := (SiteSettings{PreviewBaseURL: base}).normalize(); err == nil {
				t.Errorf("%q against the site %q must be refused", base, site)
			}
		}
		if _, err := (SiteSettings{PreviewBaseURL: "preview.openrepl.com"}).normalize(); err != nil {
			t.Errorf("a separate host name against %q: %v", site, err)
		}
	}
	t.Setenv("OPENREPL_PUBLIC_URL", "")
	// it reaches the page (settings.js) and the workers, and a change is told in the log of changes
	s := SiteSettings{PreviewBaseURL: "https://preview.example.test"}
	if s.public().PreviewBaseURL != "https://preview.example.test" {
		t.Error("the page does not get it")
	}
	if ch := settingsChanges(SiteSettings{}, s); len(ch) == 0 {
		t.Error("the change is not told")
	}
}
