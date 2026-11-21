package appstore

import (
	"strings"
	"testing"
)

// The checks of the catalogue file itself (catalogue_default.json): the sizes in it were measured by scripts/measure-catalogue.sh, and what the store
// shows and chooses follows them.
func TestCatalogueFileOptions(t *testing.T) {
	entries, _ := DefaultCatalogue()
	var amd64 bool
	for _, e := range entries {
		if err := ValidateEntry(e); err != nil {
			t.Errorf("%s: %v", e.ID, err)
		}
		if e.Minimal != nil && e.Minimal.Size[ArchAMD64] > 0 {
			amd64 = true
		}
	}
	for _, e := range entries {
		if e.Minimal == nil {
			continue
		}
		o, ok := e.OptionOf(e.Minimal.Manager)
		if !ok {
			t.Errorf("%s: the smallest option %s is not listed", e.ID, e.Minimal.Manager)
			continue
		}
		// no graphical program in the catalogue: the option that is chosen has no graphics library
		if o.GUI != "" {
			t.Errorf("%s: the chosen option (%s) has a graphics library (%s)", e.ID, o.Manager, o.GUI)
		}
		if e.Manager != e.Minimal.Manager {
			t.Errorf("%s: the manager is %s but the smallest option is %s", e.ID, e.Manager, e.Minimal.Manager)
		}
		if _, ok := o.Size[ArchARM64]; !ok {
			t.Errorf("%s: no size of the smallest option for %s", e.ID, ArchARM64)
		}
		if _, ok := o.Size[ArchAMD64]; amd64 && !ok {
			t.Errorf("%s: no size of the smallest option for %s", e.ID, ArchAMD64)
		}
		if want, ok := ChooseAll(e); ok && want != e.Manager {
			t.Errorf("%s: the sizes say %s is the smallest, the entry has %s", e.ID, want, e.Manager)
		}
		for _, x := range e.Options {
			if x.Manager != e.Manager && x.Usable(ArchARM64) && o.Size[ArchARM64] > x.Size[ArchARM64]*aptGraceFactor+aptGraceMB && x.Manager != MgrApt {
				t.Errorf("%s: %s is smaller (%.1f MB) than the chosen %s (%.1f MB)", e.ID, x.Manager, x.Size[ArchARM64], e.Manager, o.Size[ArchARM64])
			}
		}
		if e.InstMB > 0 && float64(e.InstMB) < e.ShownMB()-1 {
			t.Errorf("%s: the shown size %d is below the measured one", e.ID, e.InstMB)
		}
	}
}

// Every tool that apt could install says how: no entry of the file lists an apt option without a package name that the archive pattern accepts.
func TestCatalogueFileAptNames(t *testing.T) {
	entries, _ := DefaultCatalogue()
	for _, e := range entries {
		if o, ok := e.OptionOf(MgrApt); ok {
			if o.Package != "" && !ValidApt(o.Package) {
				t.Errorf("%s: %q is not an apt package name", e.ID, o.Package)
			}
			if e.Kind != CKTool {
				t.Errorf("%s: only a program has an apt option (not a %s)", e.ID, e.Kind)
			}
		}
	}
}

// The logos of the starter catalogue: the official icon of a project, as a link to the Simple Icons CDN. 53 of the entries have one (their project has an
// icon of its own there); the others keep the first letter. A language mark is never a stand-in (no Rust logo on a crate, no Go logo on a Go module).
func TestCatalogueFileLogos(t *testing.T) {
	entries, _ := DefaultCatalogue()
	const cdn = "https://cdn.simpleicons.org/"
	n := 0
	for _, e := range entries {
		if e.Logo == "" {
			continue
		}
		n++
		name := strings.TrimPrefix(e.Logo, cdn)
		if name == e.Logo || name == "" || strings.ContainsAny(name, "/?#: ") {
			t.Errorf("%s: the logo %q is not a link of the form %s<name>", e.ID, e.Logo, cdn)
		}
		switch name {
		case "rust", "go", "gnubash", "nodedotjs", "perl", "llvm", "json", "yaml", "markdown", "apache", "google":
			t.Errorf("%s: %q is a language, a format or a company mark, not the icon of the project itself", e.ID, name)
		}
		if e.Kind == "plugin" {
			t.Errorf("%s: a plugin of ours has no logo here", e.ID)
		}
	}
	if n != 53 {
		t.Errorf("%d entries have a logo, want 53: add it here when a project's own icon is added to the file", n)
	}
	// the same project shows the same logo wherever it is listed
	want := map[string]string{"npm:axios": "axios", "web:axios": "axios", "web:react": "react", "web:react-dom": "react", "numpy": "numpy", "py27-numpy": "numpy"}
	for _, e := range entries {
		if w, ok := want[e.ID]; ok && e.Logo != cdn+w {
			t.Errorf("%s: logo %q, want %s%s", e.ID, e.Logo, cdn, w)
		}
	}
}

func TestNormalizeLogoAndValidateLogo(t *testing.T) {
	for in, want := range map[string]string{
		"":                                  "",
		"  simple:numpy ":                   "https://cdn.simpleicons.org/numpy",
		"simple:NumPy":                      "https://cdn.simpleicons.org/numpy",
		"https://example.org/a b.svg":       "https://example.org/a b.svg",
		" https://thesvg.org/x.svg ":        "https://thesvg.org/x.svg",
		"http://insecure.example/x.png":     "http://insecure.example/x.png",
		"simple:":                           "simple:",
		"simple:not a name":                 "simple:not a name",
		"https://cdn.simpleicons.org/axios": "https://cdn.simpleicons.org/axios",
	} {
		if got := NormalizeLogo(in); got != want {
			t.Errorf("NormalizeLogo(%q) = %q, want %q", in, got, want)
		}
	}
	e := Entry{ID: "x", Name: "x", Kind: "lib", Repls: []string{"python"}, Level: "optional", Manager: "pixi", Install: "anyone"}
	ok := func(logo string) error { e.Logo = logo; return ValidateEntry(e) }
	for _, good := range []string{"", "https://anything.example/logo.png", "http://x.example/a.svg", "/images/a.svg", "https://cdn.simpleicons.org/axios"} {
		if err := ok(good); err != nil {
			t.Errorf("the logo %q was refused: %v", good, err)
		}
	}
	for _, bad := range []string{"simple:not a name", "simple:", "https://x.example/a\nb", "https://x.example/" + strings.Repeat("a", 2100)} {
		if err := ok(bad); err == nil {
			t.Errorf("the logo %q was accepted", bad[:min(len(bad), 30)])
		}
	}
}
