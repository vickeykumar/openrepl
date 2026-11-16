package appstore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The editor plugins that come with the server (the store's "Editor plugins" section). A plugin is a small script, written for this
// store, that runs in a sandboxed frame of the user's page: it has no access to the page, the account or the network. It can do two
// things through the api it is given: ask for the text of the editor (the user's own) and show a line of text in the status bar.
// Plugins are installed by an admin, not by users, and nothing is downloaded: they are made from what is in this file.

// Plugin is one that comes with the server.
type Plugin struct {
	Name    string
	Version string
	Summary string
	Main    string // the script: it defines function openreplPlugin(api)
}

// Plugins is the catalog of the plugins that come with the server (samples).
var Plugins = []Plugin{
	{
		Name: "wordcount", Version: "1.0.0",
		Summary: "Shows the words, lines and characters of the editor in the status bar.",
		Main: `function openreplPlugin(api) {
  api.every(1000, function () {
    api.text().then(function (t) {
      var words = (t.trim().match(/\S+/g) || []).length;
      api.status(words + " words · " + t.split("\n").length + " lines · " + t.length + " chars");
    });
  });
}
`,
	},
	{
		Name: "clock", Version: "1.0.0",
		Summary: "Shows the time in the status bar, for a long session.",
		Main: `function openreplPlugin(api) {
  api.every(1000, function () { api.status(new Date().toLocaleTimeString()); });
}
`,
	},
}

// PluginByName finds a plugin of the catalog.
func PluginByName(name string) (Plugin, bool) {
	for _, p := range Plugins {
		if p.Name == name {
			return p, true
		}
	}
	return Plugin{}, false
}

// SampleLanguages are the languages the store offers as examples of the "Languages" kind: conda-forge packages that bring a
// program a terminal can run. They are installed by an admin (a user asks), and are on the PATH of the users that switch them on.
var SampleLanguages = []Found{
	{Name: "lua", Version: "", Summary: "The Lua language and its interpreter.", Kind: KindLang},
	{Name: "ruby", Version: "", Summary: "The Ruby language and its interpreter.", Kind: KindLang},
	{Name: "julia", Version: "", Summary: "The Julia language, for numbers and science.", Kind: KindLang},
	{Name: "r-base", Version: "", Summary: "The R language and its base packages, for statistics.", Kind: KindLang},
}

// SampleNode are the Node libraries on the default safe list.
var SampleNode = []string{"express", "lodash", "axios", "moment"}

// PluginInstaller makes the bundle of a plugin from the catalog.
type PluginInstaller struct{}

func (PluginInstaller) Name() string { return "editor plugins that come with the server" }

func (PluginInstaller) Resolve(ctx context.Context, name, version, kind string) (Resolved, error) {
	p, ok := PluginByName(name)
	if !ok {
		return Resolved{}, fmt.Errorf("there is no plugin called %s", name)
	}
	return Resolved{Version: p.Version, Packages: 1}, nil
}

func (PluginInstaller) Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error {
	p, ok := PluginByName(name)
	if !ok || p.Version != version {
		return fmt.Errorf("there is no plugin %s %s", name, version)
	}
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	meta, _ := json.Marshal(map[string]string{"name": p.Name, "version": p.Version, "summary": p.Summary, "kind": KindPlugin})
	if err := os.WriteFile(filepath.Join(dest, "plugin.json"), meta, 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dest, "main.js"), []byte(p.Main), 0644)
}

func (PluginInstaller) Search(ctx context.Context, q, kind string) ([]Found, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	var out []Found
	for _, p := range Plugins {
		if strings.HasPrefix(p.Name, q) {
			out = append(out, Found{Name: p.Name, Version: p.Version, Summary: p.Summary, Kind: KindPlugin})
		}
	}
	return out, nil
}

// PluginCode reads the script of an installed plugin bundle (at most 64 KB).
func (s Store) PluginCode(id string) (string, error) {
	p := s.BundlePath(id)
	if p == "" || !s.HasBundle(id) {
		return "", fmt.Errorf("%s is not installed", id)
	}
	raw, err := os.ReadFile(filepath.Join(p, "main.js"))
	if err != nil {
		return "", err
	}
	if len(raw) > 64<<10 {
		return "", fmt.Errorf("the plugin is too big")
	}
	return string(raw), nil
}
