package appstore

import (
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// Install options (LLD 18, "minimal installs"): an entry lists the managers that can install it, with the size each gave when it was measured
// (after the trim, for each CPU type), and says which one is chosen. The choice is data written by the measurement script, never made at install time.

// The two CPU types the sizes are kept for.
const (
	ArchAMD64 = "linux-64"
	ArchARM64 = "linux-aarch64"
)

// Option is one way to install an entry.
type Option struct {
	Manager    string             `json:"manager"`
	Package    string             `json:"package,omitempty"`    // the name at this manager
	MinVersion string             `json:"minVersion,omitempty"` // the oldest version that is good enough for the entry; an older one makes the option unusable
	Build      string             `json:"build,omitempty"`      // pixi: a build string that pins a variant (for example one without a GUI)
	Keep       []string           `json:"keep,omitempty"`       // paths (globs, relative to the bundle) the trim leaves alone, on top of the built-in keep-list
	Full       bool               `json:"full,omitempty"`       // no trim: the app failed its check after the trim
	Env        map[string]string  `json:"env,omitempty"`        // variables the bundle needs; {bundle} stands for the folder of the bundle
	GUI        string             `json:"gui,omitempty"`        // the package that made the GUI check refuse this option ("" = none)
	Err        string             `json:"err,omitempty"`        // why it could not be measured or used
	Version    string             `json:"version,omitempty"`    // the version the sizes are of
	Size       map[string]float64 `json:"size,omitempty"`       // MB installed after the trim, by CPU type
	Before     map[string]float64 `json:"before,omitempty"`     // MB before the trim (for the admin)
	Estimate   bool               `json:"estimate,omitempty"`   // the size comes from a dry run (apt plan, pixi solve), not from a measured install
	Includes   string             `json:"includes,omitempty"`   // "its own Python and Perl (about 120 MB)": what a bundle brings that the user may not expect
}

// Minimal says which option is the smallest and what the others gave, so the choice can be seen and checked again.
type Minimal struct {
	Manager  string                        `json:"manager"`
	Size     map[string]float64            `json:"size"`             // MB by CPU type
	Others   map[string]map[string]float64 `json:"others,omitempty"` // manager -> CPU type -> MB, of the options that were tried and not chosen
	At       string                        `json:"at,omitempty"`
	Estimate bool                          `json:"estimate,omitempty"` // the sizes are estimates (a dry run): "Measure" replaces them with real ones
}

// OptionOf finds the option of a manager.
func (e Entry) OptionOf(manager string) (Option, bool) {
	for _, o := range e.Options {
		if o.Manager == manager {
			return o, true
		}
	}
	return Option{}, false
}

// Usable says whether an option may be chosen: measured with no error, no GUI library, and for the CPU type a size.
func (o Option) Usable(arch string) bool {
	if o.Err != "" || o.GUI != "" {
		return false
	}
	_, ok := o.Size[arch]
	return ok
}

// aptGrace is the allowance that apt gets over the smallest other option: 20 percent and 5 MB. A tool from the distribution is preferred to a
// build of the same version from another place when the sizes are close (it is the same libraries that the image has and a smaller risk).
const aptGraceFactor, aptGraceMB = 1.2, 5.0

// Choose picks the manager of an entry for a CPU type: the smallest usable option, apt winning when it is within the grace of the smallest
// other. ok is false when no option has a size for that CPU type (the entry keeps the manager it has).
func Choose(e Entry, arch string) (manager string, ok bool) {
	var best *Option
	var apt *Option
	opts := append([]Option(nil), e.Options...)
	for i := range opts {
		o := &opts[i]
		if !o.Usable(arch) {
			continue
		}
		if o.Manager == MgrApt {
			apt = o
			continue
		}
		if best == nil || o.Size[arch] < best.Size[arch] || (o.Size[arch] == best.Size[arch] && o.Manager < best.Manager) {
			best = o
		}
	}
	switch {
	case apt != nil && (best == nil || apt.Size[arch] <= best.Size[arch]*aptGraceFactor+aptGraceMB):
		return MgrApt, true
	case best != nil:
		return best.Manager, true
	}
	return "", false
}

// ChooseAll chooses for both CPU types: the manager must be the same on both (a bundle of one manager is made on every node, whatever its
// CPU), so an option that is usable on one CPU type only is left out, and the smaller on the sum of the two decides.
func ChooseAll(e Entry) (manager string, ok bool) {
	var both []Option
	for _, o := range e.Options {
		if o.Usable(ArchAMD64) && o.Usable(ArchARM64) {
			both = append(both, o)
		}
	}
	if len(both) == 0 {
		// only one CPU type was measured (the other run has not been made yet): choose by what there is
		for _, a := range []string{ArchARM64, ArchAMD64} {
			if m, ok := Choose(e, a); ok {
				return m, true
			}
		}
		return "", false
	}
	e.Options = both
	sum := func(o Option) float64 { return o.Size[ArchAMD64] + o.Size[ArchARM64] }
	var apt, best *Option
	for i := range both {
		o := &e.Options[i]
		if o.Manager == MgrApt {
			apt = o
		} else if best == nil || sum(*o) < sum(*best) {
			best = o
		}
	}
	if apt != nil && (best == nil || sum(*apt) <= sum(*best)*aptGraceFactor+2*aptGraceMB) {
		return MgrApt, true
	}
	return best.Manager, true
}

// ---- the GUI check -------------------------------------------------------------------------------------------------------------

var aptGUIName = regexp.MustCompile(`^(libx11|libxcb|libxau|libxdmcp|libxext|libxrender|libxrandr|libxft|libxi6|libxt6|libxmu|libxpm|libxaw|libxcursor|libxfixes|libxinerama|libxkb|libxss|libxtst|libxxf86|x11-|xorg|xserver|xkb-data|libgtk|libgdk|libqt|qt[0-9]|libgl1|libglx|libegl|libgles|libglvnd|libgl-|mesa-|libwayland|libdrm|libvulkan|libsdl|libwx|libfltk)`)

// aptGUISection are the sections of the Debian archive that hold graphical programs and their libraries.
var aptGUISection = map[string]bool{"x11": true, "gnome": true, "kde": true, "graphics": true, "libs/x11": true}

// AptPkg is a package of an apt plan.
type AptPkg struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Section string `json:"section,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	Arch    string `json:"arch,omitempty"`   // the CPU type the checksum is of: another CPU type has its own file, so it is not checked there
	SizeKB  int    `json:"sizeKB,omitempty"` // installed size
}

// GUIOfApt names the first package of a set that is a GUI library or program ("" when none): by name, or by the section of the archive.
func GUIOfApt(pkgs []AptPkg) string {
	for _, p := range pkgs {
		sec := strings.ToLower(p.Section)
		if i := strings.LastIndexByte(sec, '/'); i >= 0 {
			sec = sec[i+1:]
		}
		if aptGUIName.MatchString(p.Name) || aptGUISection[sec] {
			return p.Name
		}
	}
	return ""
}

var pixiGUIName = regexp.MustCompile(`^(xorg-|qt($|-|[0-9])|qt[0-9]|pyqt|pyside|gtk[0-9]*($|-)|libgtk|libx11|libxcb|libxau|libxdmcp|libxext|libxrender|libxft|xorg|mesa|libgl($|-)|libegl|libglvnd|libgles|wayland|libwayland|libdrm|libvulkan|sdl[0-9]*$|wxwidgets|fltk)`)

var pixiLockName = regexp.MustCompile(`/([A-Za-z0-9._+-]+?)-[0-9][^/]*?-[^/-]*\.(?:conda|tar\.bz2)\b`)

// PixiLockNames lists the package names of a pixi.lock (the URLs of its packages).
func PixiLockNames(lock string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range pixiLockName.FindAllStringSubmatch(lock, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

// GUIOfPixi names the first package of a lock that is a GUI library ("" when none).
func GUIOfPixi(names []string) string {
	for _, n := range names {
		if pixiGUIName.MatchString(strings.ToLower(n)) {
			return n
		}
	}
	return ""
}

var optionBuild = regexp.MustCompile(`^[A-Za-z0-9_.*]{1,80}$`)

// ValidateOptions says what is wrong with the options of an entry, or nil: only known managers, names that match the patterns, keep paths inside the
// bundle, variables that a bundle may set, and a minimal that names one of the options.
func ValidateOptions(e Entry) error {
	seen := map[string]bool{}
	for _, o := range e.Options {
		if !inList(validManagers, o.Manager) || o.Manager == MgrOurs {
			return fmt.Errorf("the install option %q is not one of the managers", o.Manager)
		}
		if seen[o.Manager] {
			return fmt.Errorf("the manager %q is listed twice", o.Manager)
		}
		seen[o.Manager] = true
		if o.Package != "" && (o.Manager == MgrApt && !ValidApt(o.Package) || len(o.Package) > 200 || strings.ContainsAny(o.Package, " \t\r\n\"'`$;|&<>\\")) {
			return fmt.Errorf("the package %q of the %s option is not usable", o.Package, o.Manager)
		}
		if o.MinVersion != "" && !ValidVersion(o.MinVersion) {
			return fmt.Errorf("%q is not a version", o.MinVersion)
		}
		if o.Build != "" && (o.Manager != MgrPixi || !optionBuild.MatchString(o.Build)) {
			return fmt.Errorf("the build %q is not usable", o.Build)
		}
		for _, k := range o.Keep {
			if k == "" || strings.HasPrefix(k, "/") || strings.Contains(k, "..") || strings.ContainsAny(k, "\\\r\n") {
				return fmt.Errorf("the keep path %q is not usable", k)
			}
		}
		for k, v := range o.Env {
			if !ValidExtra(k, strings.ReplaceAll(v, "{bundle}", "/store/apps/x")) {
				return fmt.Errorf("the variable %q of the %s option is not allowed", k, o.Manager)
			}
		}
		for _, m := range []map[string]float64{o.Size, o.Before} {
			for a, v := range m {
				if (a != ArchAMD64 && a != ArchARM64) || v < 0 || v > 1e6 {
					return fmt.Errorf("the size of the %s option for %q is not usable", o.Manager, a)
				}
			}
		}
		if len(o.Includes) > 200 || strings.ContainsAny(o.Includes, "<>\r\n") {
			return errors.New("the includes text is not usable")
		}
	}
	if e.Minimal != nil && !seen[e.Minimal.Manager] {
		return fmt.Errorf("the smallest option %q is not one of the options", e.Minimal.Manager)
	}
	return nil
}

// GUIName says whether a package name is a graphics library, for the manager's own naming (an old measurement may have noted one that the rules no
// longer count: tk comes with every conda Python and is not a graphical program).
func GUIName(manager, name string) bool {
	if manager == MgrApt {
		return aptGUIName.MatchString(name)
	}
	return pixiGUIName.MatchString(strings.ToLower(name))
}

// thisArch is the CPU type of this machine, as the options name it.
func thisArch() string {
	if runtime.GOARCH == "arm64" {
		return ArchARM64
	}
	return ArchAMD64
}
