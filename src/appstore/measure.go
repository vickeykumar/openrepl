package appstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Measuring (LLD 18, "minimal installs"): every manager that can install an entry is run for real, into a folder of its own, the bundle is trimmed
// and checked, and its size after the trim is kept in the entry's options for this CPU type. The script (tools/measure) and the admin's "Measure"
// button run this same code; the numbers are never typed by hand.

// MeasureEnv is what a measurement needs: a service whose installer has the managers, a folder to work in, and which apt packages exist.
type MeasureEnv struct {
	Svc  *Service
	Work string // a scratch folder (bundles are made in it and removed)
	Arch string // ArchAMD64 or ArchARM64: the CPU type of this machine
	// AptPackage says whether the Ubuntu archive has the package, "" when it has not
	AptPackage func(pkg string) bool
}

// Candidates lists the options to try for an entry: the ones it has, the manager of its kind, and apt for a program the archive has.
func (m MeasureEnv) Candidates(e Entry) []Option {
	opts := append([]Option(nil), e.Options...)
	has := func(mgr string) bool {
		for _, o := range opts {
			if o.Manager == mgr {
				return true
			}
		}
		return false
	}
	own := e.Manager
	if own == "" {
		own = MgrPixi
	}
	if own != MgrOurs && !has(own) {
		opts = append(opts, Option{Manager: own})
	}
	if e.Kind == CKTool && !has(MgrApt) && m.AptPackage != nil {
		for _, pkg := range []string{e.Package, e.Name} {
			if pkg != "" && m.AptPackage(pkg) {
				opts = append(opts, Option{Manager: MgrApt, Package: pkg})
				break
			}
		}
	}
	return opts
}

// Measure runs every candidate of the entry and returns the entry with its options and its minimal filled in for this CPU type.
// The sizes the entry already has for the other CPU type are kept.
func (m MeasureEnv) Measure(ctx context.Context, e Entry) Entry {
	cands := m.Candidates(e)
	e.Options = cands
	// the apt candidates are known to the installer through the catalogue it looks the entry up in
	m.Svc.AddEntry("measure", e)
	kind := e.InstallKind()
	for i := range e.Options {
		o := &e.Options[i]
		m.measureOne(ctx, e, o, kind)
	}
	if mgr, ok := ChooseAll(e); ok {
		e = applyChoice(e, mgr)
	}
	e.Minimal = minimalOf(e)
	return e
}

func (m MeasureEnv) measureOne(ctx context.Context, e Entry, o *Option, kind string) {
	if o.Size == nil {
		o.Size = map[string]float64{}
	}
	if o.Before == nil {
		o.Before = map[string]float64{}
	}
	delete(o.Size, m.Arch)
	delete(o.Before, m.Arch)
	o.Err, o.GUI, o.Estimate = "", "", false
	a := App{Name: e.PackageName(), Kind: kind}
	mctx := WithManager(ctx, o.Manager)
	res, err := m.Svc.cfg.Installer.Resolve(mctx, a.Name, "", kind)
	if err != nil {
		o.Err = tail(err.Error(), 300)
		return
	}
	o.GUI = res.GUI // the size is measured all the same: the admin sees what the option would cost
	a.Version, a.ID = res.Version, BundleID(a.Name, res.Version)
	dest := filepath.Join(m.Work, strings.ReplaceAll(o.Manager, " ", "-")+"-"+a.ID)
	os.RemoveAll(dest)
	defer os.RemoveAll(dest)
	var logb workLog
	start := time.Now()
	rep, full, err := m.Svc.installBundle(ctx, a, o.Manager, dest, &logb)
	if err != nil {
		o.Err = tail(strings.TrimSpace(err.Error()+" "+logb.String()), 300)
		return
	}
	_ = start
	o.Version = res.Version
	o.Includes = includesOf(dest)
	o.Before[m.Arch] = round1(rep.BeforeMB)
	o.Size[m.Arch] = round1(dirMB(dest))
	if rep.BeforeMB == 0 {
		o.Before[m.Arch] = o.Size[m.Arch]
	}
	if full {
		o.Full = true
		o.Size[m.Arch] = round1(dirMB(dest))
	}
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

// applyChoice makes the chosen option the entry's own manager and package.
func applyChoice(e Entry, mgr string) Entry {
	e.Manager = mgr
	if o, ok := e.OptionOf(mgr); ok && o.Package != "" && mgr == MgrApt {
		// the entry's own Package names the pixi/npm package: the apt name stays on the option
	}
	return e
}

// minimalOf writes the summary of the choice: the smallest option and the sizes of the others.
func minimalOf(e Entry) *Minimal {
	ch := e.Manager
	o, ok := e.OptionOf(ch)
	if !ok || len(o.Size) == 0 {
		return nil
	}
	min := &Minimal{Manager: ch, Size: map[string]float64{}, At: time.Now().UTC().Format("2006-01-02")}
	for a, v := range o.Size {
		min.Size[a] = v
	}
	for _, x := range e.Options {
		if x.Manager == ch || len(x.Size) == 0 {
			continue
		}
		if min.Others == nil {
			min.Others = map[string]map[string]float64{}
		}
		min.Others[x.Manager] = x.Size
	}
	return min
}

// ShownMB is the size the store shows for an entry: the smallest option's size (the larger of the two CPU types), or the old estimate.
func (e Entry) ShownMB() float64 {
	if e.Minimal != nil {
		var mx float64
		for _, v := range e.Minimal.Size {
			if v > mx {
				mx = v
			}
		}
		if mx > 0 {
			return mx
		}
	}
	return float64(e.InstMB)
}

// AptHas says whether the Ubuntu archive has a package that is not part of the image (apt-cache policy names a candidate).
func AptHas(pkg string) bool {
	if !ValidApt(pkg) {
		return false
	}
	out, err := exec.Command("apt-cache", "policy", pkg).Output()
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "Candidate:") {
			return !strings.Contains(l, "(none)")
		}
	}
	return false
}

// SizeTable renders the sizes of entries as text for the report: one row per entry, the smallest first.
func SizeTable(entries []Entry, arch string) string {
	var rows []string
	for _, e := range entries {
		var parts []string
		var opts []Option
		opts = append(opts, e.Options...)
		sort.Slice(opts, func(i, j int) bool { return opts[i].Manager < opts[j].Manager })
		for _, o := range opts {
			switch {
			case o.GUI != "":
				parts = append(parts, fmt.Sprintf("%s: GUI (%s)", o.Manager, o.GUI))
			case o.Err != "":
				parts = append(parts, fmt.Sprintf("%s: failed", o.Manager))
			default:
				parts = append(parts, fmt.Sprintf("%s: %.1f MB", o.Manager, o.Size[arch]))
			}
		}
		rows = append(rows, fmt.Sprintf("%-14s chosen %-6s | %s", e.Name, e.Manager, strings.Join(parts, " | ")))
	}
	return strings.Join(rows, "\n")
}

// MergeMeasured puts the measurements of the CPU types into a catalogue file: for each entry the options are joined by manager (the sizes of
// each CPU type side by side), the smallest is chosen for both CPU types, and the shown size follows. base is the JSON of the file; parts are
// the entries of each run (each has the sizes of its own CPU type). The result is the JSON of the file, in the same layout.
func MergeMeasured(base []byte, parts ...[]Entry) ([]byte, error) {
	var f catalogueFile
	if err := json.Unmarshal(base, &f); err != nil {
		return nil, err
	}
	entries := f.Entries
	got := map[string][]Entry{}
	archs := map[string]bool{} // the CPU types the runs measured
	for _, p := range parts {
		for _, e := range p {
			got[e.ID] = append(got[e.ID], e)
			for _, o := range e.Options {
				for a := range o.Size {
					archs[a] = true
				}
			}
		}
	}
	for i, e := range entries {
		runs := got[e.ID]
		if len(runs) == 0 {
			continue
		}
		var merged []Option
		for _, run := range runs {
			for _, o := range run.Options {
				k := -1
				for j := range merged {
					if merged[j].Manager == o.Manager {
						k = j
					}
				}
				if k < 0 {
					merged = append(merged, Option{Manager: o.Manager, Package: o.Package, MinVersion: o.MinVersion, Build: o.Build, Keep: o.Keep, Env: o.Env, Links: o.Links, Includes: o.Includes})
					k = len(merged) - 1
				}
				m := &merged[k]
				m.Full = m.Full || o.Full
				if m.Includes == "" {
					m.Includes = o.Includes
				}
				if m.Size == nil {
					m.Size, m.Before = map[string]float64{}, map[string]float64{}
				}
				for a, v := range o.Size {
					m.Size[a] = v
				}
				for a, v := range o.Before {
					m.Before[a] = v
				}
				if o.Version != "" {
					m.Version = o.Version
				}
				if o.GUI != "" {
					m.GUI = o.GUI
				}
				if o.Err != "" && m.Err == "" {
					m.Err = o.Err
				}
			}
		}
		// what the rules of today say of a note of the measurement, and no option that only failed (a failure in the image of the run says nothing
		// about the entry: it is listed by the merge program and the entry keeps what it had)
		var kept []Option
		for _, o := range merged {
			if o.GUI != "" && !GUIName(o.Manager, o.GUI) {
				o.GUI = ""
			}
			if o.Err != "" && len(o.Size) == 0 {
				continue
			}
			// with both CPU types measured, an option that has a size for one only failed on the other: a bundle of one manager is made on every
			// node, so it is not claimed (the entry keeps what it had; the merge program lists it)
			if len(archs) > 1 {
				missing := false
				for a := range archs {
					if _, ok := o.Size[a]; !ok {
						missing = true
					}
				}
				if missing {
					continue
				}
			}
			o.Err = ""
			kept = append(kept, o)
		}
		e.Options = kept
		if len(kept) == 0 {
			e.Minimal = nil
			entries[i] = e
			continue
		}
		if mgr, ok := ChooseAll(e); ok {
			e = applyChoice(e, mgr)
			e.Minimal = minimalOf(e)
			if sz := e.ShownMB(); sz > 0 {
				e.InstMB = int(sz + 0.999)
			}
		}
		entries[i] = e
	}
	f.Entries = entries
	out, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, out, "", " "); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// NeedsDecision lists the entries whose smallest usable option still has a GUI library (or that have no usable option at all): they are not
// installed, and the owner decides about each.
func NeedsDecision(entries []Entry) []string {
	var out []string
	for _, e := range entries {
		if len(e.Options) == 0 {
			continue
		}
		var gui, usable bool
		for _, o := range e.Options {
			if o.GUI != "" && GUIName(o.Manager, o.GUI) {
				gui = true
			} else if o.Err == "" && len(o.Size) > 0 {
				usable = true
			}
		}
		if gui && !usable {
			out = append(out, e.ID)
		}
	}
	sort.Strings(out)
	return out
}

// includesOf says what a bundle brings that a user may not expect: its own Python and Perl, with their size ("its own Python and Perl (about 120 MB)").
// The text is shown on the app's page before the install.
func includesOf(dest string) string {
	root := PrefixOf(dest)
	var names []string
	var total int64
	for _, x := range []struct{ name, bin, lib string }{{"Python", "bin/python3", "lib/python3*"}, {"Perl", "bin/perl", "lib/perl5"}} {
		if _, err := os.Stat(filepath.Join(root, x.bin)); err != nil {
			continue
		}
		names = append(names, x.name)
		total += DirSize(filepath.Join(root, x.bin))
		libs, _ := filepath.Glob(filepath.Join(root, x.lib))
		for _, l := range libs {
			total += DirSize(l)
		}
	}
	if len(names) == 0 {
		return ""
	}
	mb := int(float64(total)/(1<<20)/10+0.5) * 10 // to the nearest 10 MB: it is a hint
	if mb < 10 {
		mb = 10
	}
	return fmt.Sprintf("its own %s (about %d MB)", strings.Join(names, " and "), mb)
}

// MeasureFailures lists the options that could not be measured (entry id, manager, why), for the report of the merge.
func MeasureFailures(parts ...[]Entry) []string {
	var out []string
	have := map[string]map[string]map[string]bool{} // entry -> manager -> CPU types with a size
	archs := map[string]bool{}
	for _, p := range parts {
		for _, e := range p {
			for _, o := range e.Options {
				for a := range o.Size {
					archs[a] = true
					if have[e.ID] == nil {
						have[e.ID] = map[string]map[string]bool{}
					}
					if have[e.ID][o.Manager] == nil {
						have[e.ID][o.Manager] = map[string]bool{}
					}
					have[e.ID][o.Manager][a] = true
				}
			}
		}
	}
	for id, ms := range have {
		for mgr, as := range ms {
			if len(archs) > 1 && len(as) < len(archs) {
				out = append(out, fmt.Sprintf("%s (%s): measured on one CPU type only, so it is not claimed", id, mgr))
			}
		}
	}
	sort.Strings(out)
	for _, p := range parts {
		for _, e := range p {
			for _, o := range e.Options {
				if o.Err != "" && len(o.Size) == 0 {
					out = append(out, fmt.Sprintf("%s (%s): %s", e.ID, o.Manager, oneLine(o.Err)))
				}
			}
		}
	}
	return out
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}
