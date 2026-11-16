package appstore

import "sort"

// Runtimes are the versions the image provides for each REPL (LLD 18). An app that works with a REPL is solved against them: pixi picks
// the newest version and build whose own requirements fit, so that the app is compatible with the language the user runs. A constraint is a
// conda package and a version spec; the key of the map is the REPL id of the catalogue.
//
// The toolchains themselves (Go 1.23, JDK 21, Rust 1.85) are installed by install_prerequisite.sh; the pins of the Go, Java and Rust libraries
// are chosen against whatever the image has at start, never a number written here.
//
// Python needs none here: a library is always solved with the server's Python (Pixi.Manifest). Node is not conda: Npm.Resolve picks the
// newest version whose engines.node accepts the image's Node.
var Runtimes = map[string][][2]string{
	"c":     {{"libstdcxx-ng", "<13"}, {"libgcc-ng", "<13"}}, // gcc 12 / libstdc++ 3.4.30 of the image
	"cpp":   {{"libstdcxx-ng", "<13"}, {"libgcc-ng", "<13"}},
	"perli": {{"perl", "5.34.*"}},
	"tclsh": {{"tcl", "8.6.*"}},
}

// ConstraintsFor is the list of constraints (package, spec) that an entry's REPLs put on its solve; only a library or a module (kind clib) is
// constrained: a program brings its own runtime.
func ConstraintsFor(e Entry) [][2]string {
	if e.Kind != CKCLib {
		return nil
	}
	seen := map[string]bool{}
	var out [][2]string
	for _, r := range e.Repls {
		for _, c := range Runtimes[r] {
			if c[0] != e.PackageName() && !seen[c[0]] {
				seen[c[0]] = true
				out = append(out, c)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}
