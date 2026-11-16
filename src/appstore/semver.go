package appstore

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A small reader of the version ranges of npm's "engines.node" ( ">=12", "^14.17.0 || >=16", "12.x", ">=0.10.0 <5" ), enough to pick the
// newest version of a Node library that the server's Node can run. A range it cannot read is taken as satisfied (the check step, which
// loads the library, still refuses one that does not work).

type semver [3]int

var versionNumbers = regexp.MustCompile(`^v?(\d+)(?:\.(\d+|x|X|\*))?(?:\.(\d+|x|X|\*))?`)
var spaceAfterOp = regexp.MustCompile(`(>=|<=|>|<|=|\^|~)\s+`)

// parseVersion reads "12.22.9" (or "v12.22.9"); a prerelease or build suffix is dropped. ok is false for what is not a version.
func parseVersion(s string) (semver, bool) {
	m := versionNumbers.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return semver{}, false
	}
	var v semver
	for i := 1; i <= 3; i++ {
		if m[i] == "" || m[i] == "x" || m[i] == "X" || m[i] == "*" {
			v[i-1] = 0
			continue
		}
		n, _ := strconv.Atoi(m[i])
		v[i-1] = n
	}
	return v, true
}

func (a semver) cmp(b semver) int {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// NodeSatisfies says whether a Node of version ver (such as "12.22.9") fits the range of engines.node.
func NodeSatisfies(rng, ver string) bool {
	v, ok := parseVersion(ver)
	if !ok {
		return true
	}
	rng = strings.TrimSpace(rng)
	if rng == "" || rng == "*" {
		return true
	}
	for _, group := range strings.Split(rng, "||") {
		if satisfiesAll(group, v) {
			return true
		}
	}
	return false
}

func satisfiesAll(group string, v semver) bool {
	group = spaceAfterOp.ReplaceAllString(strings.TrimSpace(group), "$1")
	toks := strings.Fields(group)
	if len(toks) == 0 {
		return true
	}
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if i+2 < len(toks) && toks[i+1] == "-" { // a hyphen range: 12 - 14
			lo, ok1 := parseVersion(t)
			hi, ok2 := parseVersion(toks[i+2])
			if ok1 && ok2 && (v.cmp(lo) < 0 || v.cmp(hi) > 0) {
				return false
			}
			i += 2
			continue
		}
		if !comparator(t, v) {
			return false
		}
	}
	return true
}

func comparator(t string, v semver) bool {
	op := ""
	for _, o := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(t, o) {
			op, t = o, t[len(o):]
			break
		}
	}
	base, ok := parseVersion(t)
	if !ok {
		return true // "*", "x" or something unreadable
	}
	m := versionNumbers.FindStringSubmatch(t)
	parts := 1
	if m != nil && m[2] != "" && !strings.ContainsAny(m[2], "xX*") {
		parts = 2
		if m[3] != "" && !strings.ContainsAny(m[3], "xX*") {
			parts = 3
		}
	}
	switch op {
	case ">=":
		return v.cmp(base) >= 0
	case ">":
		return v.cmp(base) > 0
	case "<=":
		return v.cmp(base) <= 0
	case "<":
		return v.cmp(base) < 0
	case "^":
		hi := semver{base[0] + 1, 0, 0}
		if base[0] == 0 {
			hi = semver{0, base[1] + 1, 0}
			if base[1] == 0 {
				hi = semver{0, 0, base[2] + 1}
			}
		}
		return v.cmp(base) >= 0 && v.cmp(hi) < 0
	case "~":
		return v.cmp(base) >= 0 && v.cmp(semver{base[0], base[1] + 1, 0}) < 0
	}
	// an exact version, or a partial one (12, 12.x, 12.4): every version that starts with it
	for i := 0; i < parts; i++ {
		if v[i] != base[i] {
			return false
		}
	}
	return true
}

// newestFor picks the newest version of a package whose engines.node the given Node satisfies: versions are "1.2.3" strings with
// their range (empty when the version says none). Prereleases are left out. "" when none fits.
func newestFor(versions map[string]string, node string) string {
	var list []string
	for v := range versions {
		if _, ok := parseVersion(v); ok && !strings.ContainsAny(v, "-+") {
			list = append(list, v)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		a, _ := parseVersion(list[i])
		b, _ := parseVersion(list[j])
		return a.cmp(b) > 0
	})
	for _, v := range list {
		if NodeSatisfies(versions[v], node) {
			return v
		}
	}
	return ""
}
