package appstore

import "testing"

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
