package server

// Auto model choice (the Auto entry of the model picker): the admin names a small model and a strong one, and the page picks between them
// by rules (chat-widget/src/model-router.ts), with no model asked. This file is the admin's side: the two models, who starts on Auto, and
// what the page is told. The numbers of how Auto did are on the Genie health card (genie_health.go).

import "fmt"

type autoPublic struct {
	Small   string `json:"small"`
	Strong  string `json:"strong"`
	Default string `json:"default"` // "new", "all" or "none"
}

func (g GenieSettings) autoDefault() string {
	switch g.AutoDefault {
	case "all", "none":
		return g.AutoDefault
	}
	return "new"
}

// autoPublic is what the page reads: nil when Auto is off or not set up (a model that is off for everybody cannot be chosen here).
func (g GenieSettings) autoPublic() *autoPublic {
	if !g.AutoOn || g.AutoSmall == "" || g.AutoStrong == "" {
		return nil
	}
	return &autoPublic{Small: g.AutoSmall, Strong: g.AutoStrong, Default: g.autoDefault()}
}

// normalizeAuto checks the two models: both are models of the site, they differ, and neither is switched off.
func (g *GenieSettings) normalizeAuto() error {
	switch g.AutoDefault {
	case "", "new", "all", "none":
	default:
		return fmt.Errorf("Auto is the default for %q: choose new users, everybody or nobody", g.AutoDefault)
	}
	if !g.AutoOn {
		return nil
	}
	known := map[string]bool{}
	for _, id := range g.allModelIDs() {
		known[id] = true
	}
	off := map[string]bool{}
	for _, id := range g.DisabledModels {
		off[id] = true
	}
	for _, p := range []struct{ what, id string }{{"small", g.AutoSmall}, {"strong", g.AutoStrong}} {
		switch {
		case p.id == "":
			return fmt.Errorf("Auto needs a %s model", p.what)
		case !known[p.id]:
			return fmt.Errorf("the %s model of Auto, %q, is not a model", p.what, p.id)
		case off[p.id]:
			return fmt.Errorf("the %s model of Auto, %q, is switched off in Models", p.what, p.id)
		}
	}
	if g.AutoSmall == g.AutoStrong {
		return fmt.Errorf("the small and the strong model of Auto must differ")
	}
	return nil
}
