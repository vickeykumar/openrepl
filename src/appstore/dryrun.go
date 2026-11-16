package appstore

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// The dry-run chooser (LLD 18, "minimal installs"): an entry that was never measured (one an admin added) gets its manager from the two dry runs that
// cost nothing to make: the apt plan (`apt-get -s`: what the image lacks, with its installed size) and the pixi solve (what conda-forge would bring,
// with the download size). The sizes are ESTIMATES (they say nothing of the trim or of whether the app runs), so they are marked, and "Measure" replaces
// them with real ones. A measured size is never overwritten by an estimate. The choice is made once, on the node that holds the catalogue, and is part
// of the entry (and so of what every node installs): a node never chooses for itself.

// aptHasFn says whether the Ubuntu archive has a package (a variable so that a test can stand in for apt).
var aptHasFn = AptHas

// Compared is one row of a comparison: what one manager would bring.
type Compared struct {
	Manager  string  `json:"manager"`
	SizeMB   float64 `json:"sizeMB,omitempty"`
	Version  string  `json:"version,omitempty"`
	GUI      string  `json:"gui,omitempty"`
	Err      string  `json:"err,omitempty"`
	Estimate bool    `json:"estimate"`
	Chosen   bool    `json:"chosen"`
}

// canApt says whether this node can make an apt bundle.
func (s *Service) canApt() bool {
	m, ok := s.cfg.Installer.(Multi)
	return ok && m.Apt != nil
}

// Compare runs the dry runs of every manager that can install the entry, and returns the entry with its options and its choice filled in (the
// estimate marked) and the rows for the admin. It installs nothing.
func (s *Service) Compare(ctx context.Context, e Entry) (Entry, []Compared) {
	arch := thisArch()
	env := MeasureEnv{Svc: s, Arch: arch}
	if s.canApt() {
		env.AptPackage = aptHasFn
	}
	e.Options = env.Candidates(e)
	kind := e.InstallKind()
	// the apt candidates are known to the installer through the catalogue it looks the entry up in
	s.AddEntry("system", e)
	var rows []Compared
	for i := range e.Options {
		o := &e.Options[i]
		row := Compared{Manager: o.Manager}
		if _, measured := o.Size[arch]; measured && !o.Estimate {
			row.SizeMB, row.Version = o.Size[arch], o.Version // a real measurement stays
			rows = append(rows, row)
			continue
		}
		rctx, cancel := context.WithTimeout(WithManager(ctx, o.Manager), 3*time.Minute)
		res, err := s.cfg.Installer.Resolve(rctx, e.PackageName(), "", kind)
		cancel()
		if o.Size == nil {
			o.Size = map[string]float64{}
		}
		delete(o.Size, arch)
		o.Err, o.GUI = "", ""
		if err != nil {
			o.Err = tail(err.Error(), 300)
			row.Err = firstLineOf(err.Error())
			rows = append(rows, row)
			continue
		}
		o.Size[arch], o.Version, o.GUI, o.Estimate = round1(res.DiskMB), res.Version, res.GUI, true
		if !GUIName(o.Manager, o.GUI) {
			o.GUI = ""
		}
		row.SizeMB, row.Version, row.GUI, row.Estimate = o.Size[arch], o.Version, o.GUI, true
		rows = append(rows, row)
	}
	if mgr, ok := ChooseAll(e); ok {
		e = applyChoice(e, mgr)
		e.Minimal = minimalOf(e)
		if o, ok := e.OptionOf(mgr); ok && o.Estimate && e.Minimal != nil {
			e.Minimal.Estimate = true
		}
		if sz := e.ShownMB(); sz > 0 {
			e.InstMB = int(sz + 0.999)
		}
		for i := range rows {
			rows[i].Chosen = rows[i].Manager == mgr
		}
	}
	return e, rows
}

// SummaryOf is the sentence the admin reads after a comparison.
func SummaryOf(rows []Compared) string {
	var parts []string
	chosen := ""
	for _, r := range rows {
		switch {
		case r.Err != "":
			parts = append(parts, r.Manager+": not possible ("+r.Err+")")
		default:
			t := fmt.Sprintf("%s: %.1f MB", r.Manager, r.SizeMB)
			if r.Estimate {
				t += " (estimate)"
			}
			if r.GUI != "" {
				t += ", has " + r.GUI
			}
			parts = append(parts, t)
		}
		if r.Chosen {
			chosen = r.Manager
		}
	}
	if chosen != "" {
		return strings.Join(parts, "; ") + ". Chosen: " + chosen + "."
	}
	return strings.Join(parts, "; ") + ". No choice could be made."
}

func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 140 {
		s = s[:140]
	}
	return s
}
