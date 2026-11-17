//go:build realinstall

package appstore

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The measurement of the catalogue (LLD 18, "minimal installs"): every manager that can install an entry is run for real inside the image, the bundle
// is trimmed and checked, and the size after the trim is written for this CPU type. scripts/measure-catalogue.sh runs it, and tools/merge puts the files
// of both CPU types into catalogue_default.json.
//
//	OPENREPL_MEASURE_OUT   the file to write (the entries with their options)
//	OPENREPL_MEASURE_ONLY  a comma list of entry ids (default: every entry that is installed)
//	OPENREPL_MEASURE_KINDS a comma list of kinds (default: all)
func TestMeasureCatalogue(t *testing.T) {
	out := os.Getenv("OPENREPL_MEASURE_OUT")
	if out == "" {
		t.Skip("set OPENREPL_MEASURE_OUT to measure the catalogue")
	}
	arch := ArchAMD64
	if runtime.GOARCH == "arm64" {
		arch = ArchARM64
	}
	svc, dir := realMeasureService(t)
	m := MeasureEnv{Svc: svc, Work: dir + "/measure", Arch: arch, AptPackage: AptHas}
	os.MkdirAll(m.Work, 0755)
	only := map[string]bool{}
	for _, n := range strings.Split(os.Getenv("OPENREPL_MEASURE_ONLY"), ",") {
		if n != "" {
			only[n] = true
		}
	}
	kinds := map[string]bool{}
	for _, n := range strings.Split(os.Getenv("OPENREPL_MEASURE_KINDS"), ",") {
		if n != "" {
			kinds[n] = true
		}
	}
	var done []Entry
	write := func() {
		raw, _ := json.MarshalIndent(done, "", " ")
		os.WriteFile(out, raw, 0644) // after every entry: a run that is cut short still leaves what it measured
	}
	for _, e := range svc.Catalogue() {
		if e.InstallKind() == "" || e.Kind == CKPlugin && e.Internal || e.Manager == MgrOurs {
			continue
		}
		if len(only) > 0 && !only[e.ID] || len(kinds) > 0 && !kinds[e.Kind] {
			continue
		}
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
		r := m.Measure(ctx, e)
		cancel()
		done = append(done, r)
		write()
		var parts []string
		for _, o := range r.Options {
			parts = append(parts, o.Manager+"="+sizeText(o, arch))
		}
		t.Logf("%-22s %-8s %s (%s)", e.ID, r.Manager, strings.Join(parts, " "), time.Since(start).Round(time.Second))
	}
	t.Log("\n" + SizeTable(done, arch))
}

func sizeText(o Option, arch string) string {
	switch {
	case o.GUI != "":
		return "GUI:" + o.GUI
	case o.Err != "":
		return "failed"
	}
	return strings.TrimRight(strings.TrimRight(strings.Replace(floatText(o.Size[arch]), ",", ".", 1), "0"), ".") + "MB"
}

func floatText(f float64) string { b, _ := json.Marshal(f); return string(b) }

// realMeasureService is a service with every installer the image has (pixi, npm, apt, the mirrors), on a store of its own, for the tests that install for real.
func realMeasureService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := os.Getenv("OPENREPL_REAL_STORE")
	if dir == "" {
		dir = t.TempDir()
	}
	bin := os.Getenv("OPENREPL_PIXI")
	if bin == "" {
		bin, _ = exec.LookPath("pixi")
	}
	if bin == "" {
		t.Fatal("pixi was not found: set OPENREPL_PIXI")
	}
	var svc *Service
	lookup := func(kind, app string) (Entry, bool) { return svc.EntryFor(app, kind) }
	inst := Multi{Conda: Pixi{Bin: bin, Dir: dir, Python: PythonOf("python3"), Constraints: nil, Builds: func(app string) string {
		if e, ok := svc.EntryFor(app, KindTool); ok {
			if o, ok := e.OptionOf(MgrPixi); ok {
				return o.Build
			}
		}
		return ""
	}}, Plugin: PluginInstaller{}, GoMod: GoMod{Dir: dir, Lookup: lookup}, Jar: Maven{Dir: dir, Lookup: lookup}, Crate: Cargo{Dir: dir, Lookup: lookup}, Apt: AptBundle{Dir: dir, Lookup: lookup}}
	if npm := findRealNpm(); npm != "" {
		inst.Node = Npm{Bin: npm, Dir: dir}
	}
	svc = New(Config{Store: Store{Dir: dir}, KV: newKV(), Installer: inst, Safe: func() []string { return DefaultSafeList }})
	return svc, dir
}
