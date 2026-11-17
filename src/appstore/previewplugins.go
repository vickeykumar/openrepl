package appstore

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
)

// Plugins of the preview browser (LLD 18 and LLD 17): the Viewport switcher, the Outline and the Console are ours and are part of the pane; Eruda
// and vConsole come from npm, are served by this site at /preview/lib/<package>/<file> (nothing is loaded from another site, the policy of the
// page is not loosened) and are added to the previewed page by the server when the user has them on. A user's switch is a list kept per user.

// PreviewPlugin is one plugin of the preview as a user sees it.
type PreviewPlugin struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Summary  string `json:"summary"`
	Adapter  string `json:"adapter"`
	Ours     bool   `json:"ours"`
	On       bool   `json:"on"`
	Ready    bool   `json:"ready"`              // a third party plugin is installed (ours are always there)
	Everyone bool   `json:"everyone,omitempty"` // an admin turned it on for everyone
	Docs     string `json:"docs,omitempty"`
}

func (s *Service) pluginList(key string) []string {
	var l []string
	s.get(key, &l)
	return l
}

// previewEntries are the catalogue entries that live in the preview.
func (s *Service) previewEntries() []Entry {
	var out []Entry
	for _, e := range s.Catalogue() {
		if e.Kind == CKPlugin && e.Place == "preview" {
			out = append(out, e)
		}
	}
	return out
}

func (s *Service) pluginInstalled(e Entry) bool {
	if e.Internal {
		return true
	}
	return s.bundleOf(e.PackageName()) != ""
}

// bundleOf is the id of the finished bundle of an npm package on this node: from the catalog's record where this node has one (the gateway),
// else from the folders of the store (a worker has the bundles but not the records).
func (s *Service) bundleOf(pkg string) string {
	pkg = strings.ToLower(pkg)
	name := EncodeNpmName(pkg)
	if a, ok := s.app(name); ok && a.State == StateInstalled && cleanKind(a.Kind) == KindNode && a.ID != "" && s.cfg.Store.HasBundle(a.ID) {
		return a.ID
	}
	for _, id := range s.cfg.Store.Bundles() {
		if rest := strings.TrimPrefix(id, name+"-"); rest != id && rest != "" && rest[0] >= '0' && rest[0] <= '9' {
			if fi, err := os.Stat(filepath.Join(s.cfg.Store.BundlePath(id), "node_modules", filepath.FromSlash(pkg))); err == nil && fi.IsDir() {
				return id
			}
		}
	}
	return ""
}

// pluginOn says whether a plugin is on for a user: their own switch wins, else an admin's default, else on for ours of level core.
func (s *Service) pluginOn(home string, e Entry) bool {
	on, off, def := s.pluginList("pplug:"+home), s.pluginList("pplugoff:"+home), s.pluginList("pplugdef")
	if has(off, e.ID) {
		return false
	}
	return has(on, e.ID) || has(def, e.ID) || (e.Internal && e.Level == "core")
}

// PreviewPlugins lists the plugins of the preview for a user.
func (s *Service) PreviewPlugins(home string) []PreviewPlugin {
	out := []PreviewPlugin{}
	def := s.pluginList("pplugdef")
	for _, e := range s.previewEntries() {
		out = append(out, PreviewPlugin{ID: e.ID, Name: e.Name, Summary: e.Summary, Adapter: e.Adapter, Ours: e.Internal, On: s.pluginOn(home, e), Ready: s.pluginInstalled(e),
			Everyone: has(def, e.ID), Docs: e.Docs})
	}
	return out
}

// SwitchPlugin turns a plugin of the preview on or off for a user (a third party plugin only when it is installed).
func (s *Service) SwitchPlugin(home, id string, on bool) error {
	if !ValidHome(home) {
		return ErrUnknown
	}
	e, ok := s.Entry(id)
	if !ok || e.Kind != CKPlugin || e.Place != "preview" {
		return ErrUnknown
	}
	if on && !s.pluginInstalled(e) {
		return ErrUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	onL, offL := s.pluginList("pplug:"+home), s.pluginList("pplugoff:"+home)
	del := func(l []string) []string {
		var n []string
		for _, x := range l {
			if x != id {
				n = append(n, x)
			}
		}
		return n
	}
	onL, offL = del(onL), del(offL)
	if on {
		onL = append(onL, id)
	} else {
		offL = append(offL, id)
	}
	if err := s.put("pplug:"+home, onL); err != nil {
		return err
	}
	return s.put("pplugoff:"+home, offL)
}

// SetPluginDefault turns a plugin on for everyone (or stops that). Each user can still turn it off for themselves.
func (s *Service) SetPluginDefault(admin, id string, on bool) error {
	e, ok := s.Entry(id)
	if !ok || e.Kind != CKPlugin {
		return ErrUnknown
	}
	if on && !s.pluginInstalled(e) {
		return fmt.Errorf("%s is not installed yet", e.Name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var n []string
	for _, x := range s.pluginList("pplugdef") {
		if x != id {
			n = append(n, x)
		}
	}
	if on {
		n = append(n, id)
	}
	return s.put("pplugdef", n)
}

// LibFile is the file of an installed browser library or third party plugin that the preview may serve: pkg is the package, file the path
// inside it. A plugin serves only the files its entry names; a browser library serves any file inside its package folder (its fonts too).
func (s *Service) LibFile(pkg, file string) (string, bool) {
	pkg = strings.ToLower(pkg)
	if strings.ContainsAny(pkg, "\\") || file == "" || strings.Contains(file, "..") || strings.ContainsAny(file, "\\\x00") || strings.HasPrefix(file, "/") {
		return "", false
	}
	for _, e := range s.Catalogue() {
		if e.PackageName() != pkg || (e.Kind != CKWeb && !(e.Kind == CKPlugin && !e.Internal)) {
			continue
		}
		id := s.bundleOf(pkg)
		if id == "" {
			continue
		}
		allowed := e.Kind == CKWeb
		for _, f := range e.Files {
			if f == file {
				allowed = true
			}
		}
		if !allowed {
			continue
		}
		base := filepath.Join(s.cfg.Store.BundlePath(id), "node_modules", filepath.FromSlash(e.Source()))
		p := filepath.Join(base, filepath.FromSlash(file))
		if r, err := filepath.EvalSymlinks(p); err == nil {
			if b, err := filepath.EvalSymlinks(base); err == nil && strings.HasPrefix(r, b+string(filepath.Separator)) {
				if fi, err := os.Stat(r); err == nil && fi.Mode().IsRegular() {
					return r, true
				}
			}
		}
	}
	return "", false
}

// previewStart is what a tool needs after its script has loaded.
var previewStart = map[string]string{"eruda": "eruda.init()", "vconsole": "new VConsole()"}

// PreviewHead is the HTML the server adds to a previewed page for the third party tools that are on for a user and installed: scripts of
// this site (under prefix, where the site is served), then the call that starts each one. "" when there is none.
func (s *Service) PreviewHead(home, prefix string) string {
	if !ValidHome(home) {
		return ""
	}
	var b strings.Builder
	for _, e := range s.previewEntries() {
		if e.Internal || e.Adapter != "preview-page-tool" || !s.pluginInstalled(e) || !s.pluginOn(home, e) || len(e.Files) == 0 {
			continue
		}
		init := previewStart[e.PackageName()]
		if init == "" {
			continue // a tool with no known start call is not injected
		}
		b.WriteString(`<script src="` + html.EscapeString(prefix) + `/preview/lib/` + html.EscapeString(e.PackageName()) + "/" + html.EscapeString(e.Files[0]) + `"></script>`)
		// the tool starts when the page's body is there (the script sits at the top of the head, before the body exists)
		b.WriteString(`<script>(function(){function s(){try{` + init + `}catch(e){}}if(document.readyState==="loading"){document.addEventListener("DOMContentLoaded",s)}else{s()}})()</script>`)
	}
	return b.String()
}
