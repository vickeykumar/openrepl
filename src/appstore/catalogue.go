package appstore

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The catalogue (LLD 18, "Curated catalogue"): what the store lists, as data. A default list comes with the server (catalogue_default.json,
// embedded); an admin adds, changes or hides entries, and those are kept in the catalog database (cat:<id>, catdel:<id>). Listing or
// importing an entry never installs it.

//go:embed catalogue_default.json
var defaultCatalogueJSON []byte

// The kinds of a catalogue entry.
const (
	CKLib    = "lib"    // a Python library (pixi)
	CKLib27  = "lib27"  // a Python 2.7 library (pixi, python 2.7 environment)
	CKTool   = "tool"   // a program (pixi)
	CKCLib   = "clib"   // a C/C++/Tcl/SQLite/Perl library: headers and libraries in a pixi prefix
	CKNode   = "node"   // a Node library (npm)
	CKWeb    = "weblib" // a browser library (npm), served to the preview at /preview/lib/<name>/<file>
	CKPlugin = "plugin" // a plugin of the IDE (ours, or a third party package)
	CKGoMod  = "gomod"  // a Go module (mirror folder)
	CKJar    = "jar"    // a Maven jar (mirror folder)
	CKCrate  = "crate"  // a Rust crate (mirror folder)
)

// The managers.
const (
	MgrPixi  = "pixi"
	MgrNPM   = "npm"
	MgrApt   = "apt"
	MgrGo    = "go mirror"
	MgrMaven = "maven mirror"
	MgrCargo = "cargo mirror"
	MgrOurs  = "ours"
)

// The REPL ids the catalogue may name (the list of the owner's spec), and the places of a plugin.
var CatalogueRepls = []string{"c", "cpp", "go", "yaegi", "java", "javascript", "ts-node", "jq-repl", "node", "python", "python2.7", "ipython3", "irb", "perli", "bash", "tclsh", "evcxr", "sqlite3", "rappel", "html", "css"}
var CataloguePlaces = []string{"preview", "editor", "terminal"}

// Entry is one line of the catalogue.
type Entry struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Summary   string   `json:"summary"`
	Kind      string   `json:"kind"`
	Repls     []string `json:"repls,omitempty"`
	Place     string   `json:"place,omitempty"` // a plugin: where it lives
	Level     string   `json:"level"`           // core, popular, optional
	Manager   string   `json:"manager"`
	Package   string   `json:"package,omitempty"` // the name at the manager, when it is not the name shown
	Version   string   `json:"version,omitempty"` // a pin
	Install   string   `json:"install"`           // anyone, approval, admin
	Category  string   `json:"category,omitempty"`
	RAMMB     int      `json:"ramMB,omitempty"`  // memory when used
	InstMB    int      `json:"instMB,omitempty"` // size on disk
	Docs      string   `json:"docs,omitempty"`   // an https link, or /docs/... of this site
	Licence   string   `json:"licence,omitempty"`
	Logo      string   `json:"logo,omitempty"`  // a link to a logo image (any link), drawn before the name; empty: the first letter
	Check     string   `json:"check,omitempty"` // one of CheckTemplates
	CheckArg  string   `json:"checkArg,omitempty"`
	Labels    []string `json:"labels,omitempty"`
	Publisher string   `json:"publisher,omitempty"`
	Adapter   string   `json:"adapter,omitempty"` // a plugin: editor-status, preview-toolbar, preview-page-tool, panel-frame
	Files     []string `json:"files,omitempty"`   // a browser library or a plugin: the files served
	Internal  bool     `json:"internal,omitempty"`
	Python    string   `json:"python,omitempty"`
	// the install options and the smallest of them (options.go); Manager and Package above are the chosen one
	Options []Option `json:"options,omitempty"`
	Minimal *Minimal `json:"minimal,omitempty"`
}

// Pack is a named set of entries that an admin installs in one action.
type Pack struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Repls   []string `json:"repls,omitempty"`
	Entries []string `json:"entries"`
}

type catalogueFile struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
	Packs   []Pack  `json:"packs"`
}

// CheckTemplates are the only checks an entry may name: there is no field that takes a command.
var CheckTemplates = []string{"python-import", "node-require", "version-flag", "file-exists"}

var validLevels = []string{"core", "popular", "optional"}
var validInstall = []string{"anyone", "approval", "admin"}
var validKinds = []string{CKLib, CKLib27, CKTool, CKCLib, CKNode, CKWeb, CKPlugin, CKGoMod, CKJar, CKCrate}
var validManagers = []string{MgrPixi, MgrNPM, MgrApt, MgrGo, MgrMaven, MgrCargo, MgrOurs}
var validAdapters = []string{"editor-status", "preview-toolbar", "preview-page-tool", "editor-extension", "panel-frame"}

func inList(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// DefaultCatalogue is the list that comes with the server.
func DefaultCatalogue() ([]Entry, []Pack) {
	var f catalogueFile
	if json.Unmarshal(defaultCatalogueJSON, &f) != nil {
		return nil, nil
	}
	return f.Entries, f.Packs
}

// ValidateEntry says what is wrong with an entry, or nil. Every field is checked against a fixed list or a pattern.
func ValidateEntry(e Entry) error {
	if e.ID == "" || len(e.ID) > 80 || strings.ContainsAny(e.ID, " \t\r\n\\\"'`$;|&<>") {
		return fmt.Errorf("the id %q is not usable", e.ID)
	}
	if strings.Contains(e.ID, "..") {
		return fmt.Errorf("the id %q is not usable", e.ID)
	}
	if strings.TrimSpace(e.Name) == "" || len(e.Name) > 80 || len(e.Summary) > 300 {
		return errors.New("the name or the summary is empty or too long")
	}
	if !inList(validKinds, e.Kind) {
		return fmt.Errorf("the kind %q is not one of %s", e.Kind, strings.Join(validKinds, ", "))
	}
	if !inList(validLevels, e.Level) {
		return fmt.Errorf("the level %q is not one of %s", e.Level, strings.Join(validLevels, ", "))
	}
	if !inList(validInstall, e.Install) {
		return fmt.Errorf("who may install %q is not one of %s", e.Install, strings.Join(validInstall, ", "))
	}
	if !inList(validManagers, e.Manager) {
		return fmt.Errorf("the manager %q is not one of %s", e.Manager, strings.Join(validManagers, ", "))
	}
	if e.Kind == CKPlugin {
		if !inList(CataloguePlaces, e.Place) {
			return fmt.Errorf("a plugin needs a place: %s", strings.Join(CataloguePlaces, ", "))
		}
		if e.Adapter != "" && !inList(validAdapters, e.Adapter) {
			return fmt.Errorf("the adapter type %q is not one of %s", e.Adapter, strings.Join(validAdapters, ", "))
		}
	} else {
		if len(e.Repls) == 0 {
			return errors.New("an app needs at least one REPL")
		}
		for _, r := range e.Repls {
			if !inList(CatalogueRepls, r) {
				return fmt.Errorf("%q is not a REPL of this site", r)
			}
		}
	}
	if e.Check != "" && !inList(CheckTemplates, e.Check) {
		return fmt.Errorf("the check %q is not one of %s", e.Check, strings.Join(CheckTemplates, ", "))
	}
	if strings.ContainsAny(e.CheckArg, "\r\n;|&$`<>\"'\\") {
		return errors.New("the check argument has characters that are not allowed")
	}
	if e.Version != "" && !ValidVersion(e.Version) {
		return fmt.Errorf("%q is not a version", e.Version)
	}
	if e.Docs != "" {
		if strings.HasPrefix(e.Docs, "/docs/") && !strings.Contains(e.Docs, "..") {
		} else if u, err := url.Parse(e.Docs); err != nil || u.Scheme != "https" || u.Host == "" {
			return errors.New("the documentation link must be an https address, or a /docs/ page of this site")
		}
	}
	if l := e.Logo; l != "" {
		// any link: no host list and no rule about its form (an admin's field). Only what could not be one: a line break or a control character, a name
		// that is not a Simple Icons name after "simple:", and a text that is too long to be a link.
		if len(l) > 2000 || strings.IndexFunc(l, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return errors.New("the logo is not a link")
		}
		if strings.HasPrefix(l, "simple:") {
			return errors.New("the logo name after simple: has to be letters and digits, like simple:numpy")
		}
	}
	for _, f := range e.Files {
		if f == "" || strings.HasPrefix(f, "/") || strings.Contains(f, "..") || strings.ContainsAny(f, "\\\r\n") {
			return fmt.Errorf("the file %q is not usable", f)
		}
	}
	return ValidateOptions(e)
}

// Source is the name of the package at its manager: the module path of a Go module, group:artifact of a jar, the crate, the conda package.
func (e Entry) Source() string {
	if e.Package != "" {
		return strings.ToLower(e.Package)
	}
	return strings.ToLower(e.Name)
}

// mirrorPrefix is the start of the name of an app of a kind that has a name of its own in the store: a module, a jar and a crate can have the
// name of a library of another kind (a Python and a Rust "regex"), so the app says what it is.
var mirrorPrefix = map[string]string{CKGoMod: "gomod-", CKJar: "jar-", CKCrate: "crate-", CKLib27: "py27-"}

// PackageName is the name of the app in the store (and of its bundle): the package name for most kinds, "gomod-cobra" for a Go module.
func (e Entry) PackageName() string {
	if px, ok := mirrorPrefix[e.Kind]; ok {
		n := strings.ToLower(e.Name)
		if e.Kind == CKLib27 {
			n = e.Source()
		}
		return px + n
	}
	if e.Kind == CKNode || e.Kind == CKWeb || e.Kind == CKPlugin {
		return EncodeNpmName(e.Source()) // a scoped package (@scope/name) is "scope__name" in the store: a folder name has no @ or /
	}
	return e.Source()
}

// EncodeNpmName is the name an npm package has in the store: "@picocss/pico" is "picocss__pico" (the name of an app is the name of its bundle folder).
func EncodeNpmName(pkg string) string {
	pkg = strings.ToLower(strings.TrimSpace(pkg))
	if strings.HasPrefix(pkg, "@") {
		if i := strings.IndexByte(pkg, '/'); i > 1 && i < len(pkg)-1 {
			return pkg[1:i] + "__" + pkg[i+1:]
		}
	}
	return pkg
}

// NpmName is the name npm knows an app of the store by: "picocss__pico" is "@picocss/pico".
func NpmName(app string) string {
	if i := strings.Index(app, "__"); i > 0 && i < len(app)-2 && !strings.HasPrefix(app, "@") {
		return "@" + app[:i] + "/" + app[i+2:]
	}
	return app
}

// IsMirrorKind says whether an app kind is kept in a mirror folder of the store (Go modules, jars, crates).
func IsMirrorKind(k string) bool { return k == KindGoMod || k == KindJar || k == KindCrate }

// UseLine is the first line of how to use an app once it is on, for its card: an import, a command, an include.
func (e Entry) UseLine() string {
	src := e.Source()
	switch e.Kind {
	case CKLib:
		return "import " + e.Module()
	case CKLib27:
		return "python2.7 -c \"import " + e.Module() + "\""
	case CKTool:
		if e.CheckArg != "" {
			return e.CheckArg + " --version"
		}
		return e.PackageName() + " --version"
	case CKCLib:
		switch e.Category {
		case "Perl modules":
			return "perl -e 'use " + strings.ReplaceAll(strings.TrimPrefix(e.Name, "perl-"), "-", "::") + "'"
		case "Tcl":
			return "package require " + e.Name
		case "SQLite extensions":
			return ".load <the path on the app page>"
		}
		if strings.HasPrefix(e.CheckArg, "include/") {
			return "#include <" + strings.TrimPrefix(e.CheckArg, "include/") + "/...>"
		}
		return "#include <" + e.Name + "/...>"
	case CKNode:
		return "const x = require('" + src + "')"
	case CKGoMod:
		return "go get " + src
	case CKJar:
		if e.CheckArg != "" {
			return "import " + e.CheckArg + ";"
		}
	case CKCrate:
		return ":dep " + src + " = \"*\""
	}
	return ""
}

// Module is the name a program uses to import the app: the Python module of a library, the npm package, the Go module path, the Java package
// of a jar, the Rust crate (with underscores). "" for what is not imported (a tool, a browser library, a plugin).
func (e Entry) Module() string {
	switch e.Kind {
	case CKLib, CKLib27:
		if e.CheckArg != "" {
			return e.CheckArg
		}
		return strings.NewReplacer("-", "_", ".", "_").Replace(e.Source())
	case CKNode:
		return e.Source()
	case CKGoMod:
		return e.Source()
	case CKJar:
		if i := strings.LastIndexByte(e.CheckArg, '.'); i > 0 {
			return e.CheckArg[:i]
		}
	case CKCrate:
		return strings.ReplaceAll(e.Source(), "-", "_")
	}
	return ""
}

// InstallKind is the kind the installer uses for the entry, or "" for one that cannot be installed on this server yet.
func (e Entry) InstallKind() string {
	switch e.Kind {
	case CKLib:
		return KindLib
	case CKTool, CKCLib:
		return KindTool
	case CKGoMod:
		return KindGoMod
	case CKJar:
		return KindJar
	case CKCrate:
		return KindCrate
	case CKLib27:
		return KindPy27
	case CKNode, CKWeb:
		return KindNode // a browser library is an npm package, served to the preview; the bundle is the same as a Node library's
	case CKPlugin:
		if e.Internal {
			if e.Place == "editor" {
				return KindPlugin // the samples that run in a sandboxed frame of the editor
			}
			return "" // our preview plugins are part of the page: nothing to install
		}
		return KindNode // a plugin from npm: its files are served from the bundle
	}
	return ""
}

// InstallEntry installs an entry for an admin: at its pinned version when it has one.
func (s *Service) InstallEntry(who string, e Entry) error {
	k := e.InstallKind()
	if k == "" {
		return errors.New("this kind of entry cannot be installed on this server yet")
	}
	if e.Version != "" && (k == KindNode || IsMirrorKind(k) || k == KindPy27) {
		return s.InstallVersion(who, e.PackageName(), k, e.Version)
	}
	return s.Install(who, e.PackageName(), k, true)
}

// Catalogue is the list of the entries: the default ones, with the admin's changes.
func (s *Service) Catalogue() []Entry {
	base, _ := DefaultCatalogue()
	hidden := map[string]bool{}
	s.each("catdel:", func(key string, _ []byte) { hidden[strings.TrimPrefix(key, "catdel:")] = true })
	over := map[string]Entry{}
	s.remoteMu.Lock()
	for _, e := range s.remote {
		over[e.ID] = e
	}
	s.remoteMu.Unlock()
	var added []Entry
	s.each("cat:", func(_ string, raw []byte) {
		var e Entry
		if json.Unmarshal(raw, &e) == nil && e.ID != "" {
			over[e.ID] = e
		}
	})
	out := make([]Entry, 0, len(base)+len(over))
	seen := map[string]bool{}
	for _, e := range base {
		if hidden[e.ID] {
			continue
		}
		if o, ok := over[e.ID]; ok {
			e = o
		}
		seen[e.ID] = true
		out = append(out, e)
	}
	for id, e := range over {
		if !seen[id] && !hidden[id] {
			added = append(added, e)
		}
	}
	sort.Slice(added, func(i, j int) bool { return added[i].ID < added[j].ID })
	return append(out, added...)
}

// Entry finds one entry by its id.
func (s *Service) Entry(id string) (Entry, bool) {
	for _, e := range s.Catalogue() {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// asset says whether an entry is a bundle that is served to the preview (a browser library, a plugin from npm) and not one that a terminal
// switches on.
func (e Entry) asset() bool { return e.Kind == CKWeb || (e.Kind == CKPlugin && !e.Internal) }

// EntryFor finds the entry that an installed app was made from (by the package name and the installer kind). An entry of a terminal kind
// is preferred to an asset entry of the same package.
func (s *Service) EntryFor(name, kind string) (Entry, bool) {
	var asset *Entry
	for _, e := range s.Catalogue() {
		if e.InstallKind() == kind && e.PackageName() == name {
			if e.asset() {
				e := e
				asset = &e
				continue
			}
			return e, true
		}
	}
	if asset != nil {
		return *asset, true
	}
	return Entry{}, false
}

// checkApp is the check after an install: the one the entry names where it has one (a file for a browser library or a header package, an
// import for a Python library), else the check of the kind.
func (s *Service) checkApp(ctx context.Context, dest string, a App) error {
	kind := cleanKind(a.Kind)
	e, ok := s.EntryFor(a.Name, kind)
	if ok {
		switch {
		case e.asset():
			return checkFiles(dest, e)
		case e.Kind == CKCLib && e.Check == "file-exists" && e.CheckArg != "":
			return fileExists(PrefixOf(dest), e.CheckArg)
		case e.Kind == CKTool && e.Check == "version-flag" && progPattern.MatchString(e.CheckArg):
			return verifyToolAs(ctx, dest, e.CheckArg, a.Name, a.Version) // the program the entry names, by the name a user types
		case e.Kind == CKGoMod:
			return GoMod{Dir: s.cfg.Store.Dir}.check(ctx, dest, e, a.Version)
		case e.Kind == CKJar:
			return Maven{Dir: s.cfg.Store.Dir}.check(ctx, dest, e)
		case e.Kind == CKCrate:
			return Cargo{Dir: s.cfg.Store.Dir}.check(ctx, dest, e, a.Version)
		case e.Kind == CKLib27 && identPattern.MatchString(e.CheckArg):
			py := filepath.Join(PrefixOf(dest), "bin", "python")
			cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			c := exec.CommandContext(cctx, py, "-c", "import "+e.CheckArg)
			c.Dir = os.TempDir()
			if out, err := c.CombinedOutput(); err != nil {
				return fmt.Errorf("%s did not start in Python 2.7: %s", a.Name, tail(strings.TrimSpace(string(out)), 400))
			}
			return nil
		case e.Kind == CKLib && e.Check == "python-import" && identPattern.MatchString(e.CheckArg):
			py := filepath.Join(PrefixOf(dest), "bin", "python")
			if _, err := os.Stat(py); err == nil {
				cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
				defer cancel()
				c := exec.CommandContext(cctx, py, "-I", "-c", "import "+e.CheckArg)
				c.Dir = os.TempDir()
				if out, err := c.CombinedOutput(); err != nil {
					return fmt.Errorf("%s did not start: %s", a.Name, tail(strings.TrimSpace(string(out)), 400))
				}
				return nil
			}
		}
	}
	return checkBundle(ctx, dest, a.Name, a.Kind, a.Version)
}

func fileExists(base, rel string) error {
	if strings.Contains(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("the check names a path that is not allowed")
	}
	if _, err := os.Stat(filepath.Join(base, filepath.FromSlash(rel))); err != nil {
		return fmt.Errorf("%s is not there", rel)
	}
	return nil
}

// checkFiles looks for the files a browser library or a plugin from npm serves: all of them must be in the package.
func checkFiles(dest string, e Entry) error {
	base := filepath.Join(dest, "node_modules", filepath.FromSlash(e.Source()))
	files := e.Files
	if len(files) == 0 && e.CheckArg != "" {
		files = []string{e.CheckArg}
	}
	if len(files) == 0 {
		return fileExists(base, "package.json")
	}
	for _, f := range files {
		if err := fileExists(base, f); err != nil {
			return fmt.Errorf("%s: %v", e.Name, err)
		}
	}
	return nil
}

// SimpleIconsCDN is where a logo named simple:<name> is read from: the official icon of a project, in its own colour.
const SimpleIconsCDN = "https://cdn.simpleicons.org/"

var simpleIconName = regexp.MustCompile(`^[a-z0-9]{1,60}$`)

// NormalizeLogo cleans the logo field of an entry: the space around it goes, and simple:<name> becomes the link of that icon at the Simple Icons CDN,
// so that the data always holds a link. Any other text is kept as it is (a name that is not valid is left for ValidateEntry to refuse).
func NormalizeLogo(s string) string {
	s = strings.TrimSpace(s)
	if rest := strings.TrimPrefix(s, "simple:"); rest != s {
		if n := strings.ToLower(strings.TrimSpace(rest)); simpleIconName.MatchString(n) {
			return SimpleIconsCDN + n
		}
	}
	return s
}

// AddEntry adds or replaces an entry (an admin's). Nothing is installed.
func (s *Service) AddEntry(admin string, e Entry) error {
	e.Logo = NormalizeLogo(e.Logo)
	if err := ValidateEntry(e); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.del("catdel:" + e.ID)
	return s.put("cat:"+e.ID, e)
}

// RemoveEntry takes an entry off the list (an installed app stays installed; an admin removes that apart).
func (s *Service) RemoveEntry(admin, id string) error {
	if _, ok := s.Entry(id); !ok {
		return ErrUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.del("cat:" + id)
	return s.put("catdel:"+id, true)
}

// Packs lists the packs: the default ones and the admin's.
func (s *Service) Packs() []Pack {
	_, base := DefaultCatalogue()
	over := map[string]Pack{}
	s.each("pack:", func(_ string, raw []byte) {
		var p Pack
		if json.Unmarshal(raw, &p) == nil && p.ID != "" {
			over[p.ID] = p
		}
	})
	hidden := map[string]bool{}
	s.each("packdel:", func(key string, _ []byte) { hidden[strings.TrimPrefix(key, "packdel:")] = true })
	var out []Pack
	seen := map[string]bool{}
	for _, p := range base {
		if hidden[p.ID] {
			continue
		}
		if o, ok := over[p.ID]; ok {
			p = o
		}
		seen[p.ID] = true
		out = append(out, p)
	}
	var more []Pack
	for id, p := range over {
		if !seen[id] && !hidden[id] {
			more = append(more, p)
		}
	}
	sort.Slice(more, func(i, j int) bool { return more[i].ID < more[j].ID })
	return append(out, more...)
}

// SetPack saves a pack; every entry it names has to be in the catalogue.
func (s *Service) SetPack(admin string, p Pack) error {
	p.ID = strings.ToLower(strings.TrimSpace(p.ID))
	if p.ID == "" || strings.ContainsAny(p.ID, " /\\") || strings.TrimSpace(p.Name) == "" || len(p.Entries) == 0 {
		return errors.New("a pack needs an id, a name and at least one app")
	}
	for _, id := range p.Entries {
		if _, ok := s.Entry(id); !ok {
			return fmt.Errorf("%s is not in the catalogue", id)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.del("packdel:" + p.ID)
	return s.put("pack:"+p.ID, p)
}

// RemovePack takes a pack off the list.
func (s *Service) RemovePack(admin, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.del("pack:" + id)
	return s.put("packdel:"+id, true)
}

// PackInstall installs the entries of a pack that are not installed yet, for an admin. It returns the ids it started and the ones it
// could not (with why).
func (s *Service) PackInstall(admin, id string) (started []string, skipped map[string]string, err error) {
	skipped = map[string]string{}
	var pack *Pack
	for _, p := range s.Packs() {
		if p.ID == id {
			p := p
			pack = &p
		}
	}
	if pack == nil {
		return nil, nil, ErrUnknown
	}
	for _, eid := range pack.Entries {
		e, ok := s.Entry(eid)
		if !ok {
			skipped[eid] = "not in the catalogue"
			continue
		}
		k := e.InstallKind()
		if k == "" {
			skipped[eid] = "this kind cannot be installed on this server yet"
			continue
		}
		if err := s.InstallEntry(admin, e); err != nil {
			if errors.Is(err, ErrInstalled) || errors.Is(err, ErrBusy) {
				continue
			}
			skipped[eid] = err.Error()
			continue
		}
		started = append(started, eid)
	}
	return started, skipped, nil
}

// Export is the catalogue as an admin's file: the entries that differ from the default list, and the packs.
func (s *Service) Export() ([]byte, error) {
	f := catalogueFile{Version: 1, Entries: s.Catalogue(), Packs: s.Packs()}
	return json.MarshalIndent(f, "", " ")
}

// ImportReport says what an import did.
type ImportReport struct {
	Added    int      `json:"added"`
	Packs    int      `json:"packs"`
	Rejected []string `json:"rejected"`
}

// Import reads a file made by Export. Every entry is validated like one typed in the form; one that fails is named and skipped. With
// dry set nothing is saved. Nothing is installed.
func (s *Service) Import(admin string, data []byte, dry bool) (ImportReport, error) {
	var f catalogueFile
	if len(data) > 2<<20 {
		return ImportReport{}, errors.New("the file is too big")
	}
	if err := json.Unmarshal(data, &f); err != nil || f.Version != 1 {
		return ImportReport{}, errors.New("this is not a catalogue file")
	}
	rep := ImportReport{Rejected: []string{}}
	for _, e := range f.Entries {
		if err := ValidateEntry(e); err != nil {
			rep.Rejected = append(rep.Rejected, e.ID+": "+err.Error())
			continue
		}
		if !dry {
			if err := s.AddEntry(admin, e); err != nil {
				rep.Rejected = append(rep.Rejected, e.ID+": "+err.Error())
				continue
			}
		}
		rep.Added++
	}
	for _, p := range f.Packs {
		if dry {
			ok := true
			for _, id := range p.Entries {
				found := false
				for _, e := range f.Entries {
					found = found || e.ID == id
				}
				if _, in := s.Entry(id); in || found {
					continue
				}
				ok = false
			}
			if ok {
				rep.Packs++
			} else {
				rep.Rejected = append(rep.Rejected, "pack "+p.ID+": names an app that is not in the catalogue")
			}
			continue
		}
		if err := s.SetPack(admin, p); err != nil {
			rep.Rejected = append(rep.Rejected, "pack "+p.ID+": "+err.Error())
			continue
		}
		rep.Packs++
	}
	return rep, nil
}

// Filter is what the store page asks for.
type Filter struct {
	Q, Repl, Kind, Category, Level, Place string
	Browser                               bool // only what runs in the browser (preview): browser libraries and plugins of the preview
}

// Apply keeps the entries that match every part of the filter that is set.
func (f Filter) Apply(list []Entry) []Entry {
	q := strings.ToLower(strings.TrimSpace(f.Q))
	var out []Entry
	for _, e := range list {
		if f.Repl != "" && !inList(e.Repls, f.Repl) {
			continue
		}
		if f.Kind != "" && e.Kind != f.Kind {
			continue
		}
		if f.Category != "" && !strings.EqualFold(e.Category, f.Category) {
			continue
		}
		if f.Level != "" && e.Level != f.Level {
			continue
		}
		if f.Place != "" && e.Place != f.Place {
			continue
		}
		if f.Browser && e.Kind != CKWeb && !(e.Kind == CKPlugin && e.Place == "preview") {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.Name+" "+e.Summary+" "+e.Category), q) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// DryRow is the answer for one name of a bulk add.
type DryRow struct {
	Name    string `json:"name"`
	Solves  bool   `json:"solves"`
	Version string `json:"version,omitempty"`
	SizeMB  int    `json:"sizeMB,omitempty"`
	Result  string `json:"result"`
	InCat   bool   `json:"inCatalogue,omitempty"`
}

// DryRun checks a list of names against the installer without installing anything (a bulk add, step one). The solve is the one the
// install would do, so "solves" means the install will work on this node.
func (s *Service) DryRun(ctx context.Context, kind string, names []string) []DryRow {
	var rows []DryRow
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		if len(rows) >= 40 {
			rows = append(rows, DryRow{Name: n, Result: "only 40 names at a time"})
			break
		}
		row := DryRow{Name: n}
		if !ValidName(n) {
			row.Result = "not a package name"
			rows = append(rows, row)
			continue
		}
		if b, ok := s.BuiltinNamed(n); ok {
			row.Result = "built in (" + b.Version + ")"
			rows = append(rows, row)
			continue
		}
		if _, ok := s.EntryFor(n, kind); ok {
			row.InCat = true
		}
		r, err := s.Estimate(ctx, n, kind)
		if err != nil {
			row.Result = tail(err.Error(), 160)
		} else {
			row.Solves, row.Version, row.SizeMB, row.Result = true, r.Version, int(r.DiskMB), "ready to add"
			if row.InCat {
				row.Result = "already in the catalogue"
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// BulkAdd puts the names that solved into the catalogue (kind lib, tool or node), at the level given.
func (s *Service) BulkAdd(admin string, repls []string, kind, level, manager string, rows []DryRow) (int, error) {
	added := 0
	for _, r := range rows {
		if !r.Solves || r.InCat {
			continue
		}
		e := Entry{ID: r.Name, Name: r.Name, Summary: "Added by an admin.", Kind: kindForInstall(kind), Repls: repls, Level: level, Manager: manager,
			Install: "approval", InstMB: r.SizeMB, Check: "version-flag", CheckArg: r.Name}
		if e.Kind == CKLib {
			e.Check, e.CheckArg = "python-import", strings.ReplaceAll(r.Name, "-", "_")
		}
		if e.Kind == CKNode {
			e.ID, e.Check = "npm:"+r.Name, "node-require"
		}
		if err := s.AddEntry(admin, e); err != nil {
			return added, err
		}
		added++
	}
	return added, nil
}

func kindForInstall(k string) string {
	switch k {
	case KindTool:
		return CKTool
	case KindNode:
		return CKNode
	}
	return CKLib
}
