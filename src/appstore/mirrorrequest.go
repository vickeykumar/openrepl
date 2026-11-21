package appstore

import (
	"fmt"
	"regexp"
	"strings"
)

// A request for a module, a jar or a crate that the catalogue does not have yet (found by a search, LLD 18 "Search for every kind"). The installers
// need the real package name and a check, which only a catalogue entry carries: the request keeps what the registry said, and approving it makes the
// entry (with the safest check there is: the files are there; for a module the module itself must build; a jar is checked for its files only, since
// no class is known) and installs it.

var mirrorShort = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

type mirrorKindInfo struct {
	ck, idPrefix, manager, repl, category, docs string
}

var mirrorKinds = map[string]mirrorKindInfo{
	KindGoMod: {CKGoMod, "go:", "go mirror", "go", "Go modules", "https://pkg.go.dev/"},
	KindJar:   {CKJar, "jar:", "maven mirror", "java", "Java libraries", "https://central.sonatype.com/search?q="},
	KindCrate: {CKCrate, "crate:", "cargo mirror", "evcxr", "Rust crates", "https://crates.io/crates/"},
}

// RequestMirror asks for a module, jar or crate found by a search: short is the name in the store (without its prefix), pkg what the registry calls
// it (a module path, group:artifact, a crate name).
func (s *Service) RequestMirror(who, kind, short, pkg, summary, reason string) (Request, error) {
	info, ok := mirrorKinds[kind]
	if !ok {
		return Request{}, fmt.Errorf("%q is not a kind that is searched", kind)
	}
	short = strings.ToLower(strings.TrimSpace(short))
	pkg = strings.TrimSpace(pkg)
	if !mirrorShort.MatchString(short) || pkg == "" || len(pkg) > 160 || strings.ContainsAny(pkg, " \t\r\n\\\"'`$;|&<>") {
		return Request{}, fmt.Errorf("%q is not a package of this kind", pkg)
	}
	_ = info
	name := mirrorPrefix[info.ck] + short
	r, err := s.RequestFor(who, name, kind, "", reason)
	if err != nil {
		return r, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Package == "" {
		r.Package, r.Summary = pkg, clip(summary, 200)
	}
	return r, s.put("req:"+name, r)
}

// ensureMirrorEntry makes the catalogue entry of an approved request; an entry that is already there for the same package is left as it is, one for
// another package with the same name is refused (the store name is the short one).
func (s *Service) ensureMirrorEntry(admin string, r Request) error {
	info, ok := mirrorKinds[r.Kind]
	if !ok {
		return nil
	}
	short := strings.TrimPrefix(r.Name, mirrorPrefix[info.ck])
	id := info.idPrefix + short
	if e, ok := s.Entry(id); ok {
		if e.Package != "" && e.Package != r.Package {
			return fmt.Errorf("%s is already the name of %s: this is %s", short, e.Package, r.Package)
		}
		return nil
	}
	e := Entry{ID: id, Name: short, Summary: clip(r.Summary, 280), Kind: info.ck, Level: "optional", Manager: info.manager, Install: "approval", Repls: []string{info.repl},
		Category: info.category, Package: r.Package, Docs: info.docs + r.Package, Check: "file-exists"}
	switch r.Kind {
	case KindGoMod:
		e.CheckArg = r.Package
	case KindCrate:
		e.CheckArg = "vendor"
	}
	return s.AddEntry(admin, e)
}
