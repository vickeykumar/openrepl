package appstore

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// What a user may read of an install while it runs (LLD 18, section 16): not the log, a clean extract of it. A line of a step is shown when it is an
// info line of a step the user can follow; a line of the manager's output is shown only when it has one of a few known shapes, and then only as
// "fetched <name> <version>", so a new kind of output can never leak by accident. Servers, environments, commands, error tails and paths stay
// in the admin's log.

// UserLine is one line a user reads.
type UserLine struct {
	T    int64  `json:"t"` // unix milliseconds
	Text string `json:"x"`
}

var userSteps = map[string]bool{"look up": true, "install": true, "trim": true, "check": true, "ladder": true, "result": true}

var (
	reName    = `[A-Za-z0-9@][A-Za-z0-9@._/+~:-]{0,79}`
	reGoGet   = regexp.MustCompile(`^go: (?:downloading|found) (` + reName + `) (v[0-9A-Za-z.+_-]{1,40})$`)
	reNpmGet  = regexp.MustCompile(`^npm http fetch GET 200 https?://[A-Za-z0-9.-]+/([A-Za-z0-9@%][A-Za-z0-9@._/+~:%-]{0,79}?)(?:/-/\S+)? \d+ms$`)
	reNpmDone = regexp.MustCompile(`^added (\d{1,5}) packages?\b`)
	reCargo   = regexp.MustCompile(`^\s*(Downloaded|Compiling|Checking) (` + reName + `) v([0-9A-Za-z.+_-]{1,40})`)
	reAptGet  = regexp.MustCompile(`^Get:\d+ \S+ \S+ \S+ (` + reName + `) \S+ ([0-9A-Za-z.+:~_-]{1,60})`)
)

// userText turns one log line into what a user may read, or "" when it must not be shown.
func userText(l LogLine) string {
	var t string
	switch {
	case l.Step == "output":
		o := strings.TrimSpace(l.Text)
		if m := reGoGet.FindStringSubmatch(o); m != nil {
			t = "fetched " + m[1] + " " + m[2]
		} else if m := reNpmGet.FindStringSubmatch(o); m != nil {
			name, _ := url.PathUnescape(m[1])
			t = "fetched " + name
		} else if m := reNpmDone.FindStringSubmatch(o); m != nil {
			t = "added " + m[1] + " packages"
		} else if m := reCargo.FindStringSubmatch(o); m != nil {
			verb := map[string]string{"Downloaded": "fetched", "Compiling": "building", "Checking": "checking"}[m[1]]
			t = fmt.Sprintf("%s %s %s", verb, m[2], m[3])
		} else if m := reAptGet.FindStringSubmatch(o); m != nil {
			t = "fetched " + m[1] + " " + m[2]
		}
	case userSteps[l.Step] && l.Level == "info":
		t = strings.TrimSpace(l.Text)
	}
	if t == "" || len(t) > 200 || strings.ContainsAny(t, "\x00\r\n") {
		return ""
	}
	// whatever the filters let through still shows no address and no path
	if strings.Contains(t, "://") || strings.Contains(t, "/store") || strings.HasPrefix(t, "/") || strings.Contains(t, "~/") || strings.Contains(t, "\\") {
		return ""
	}
	return t
}

// UserLines picks the lines a user may read out of the lines of an attempt (the same order), without a line that repeats the one before.
func UserLines(lines []LogLine) []UserLine {
	out := []UserLine{}
	for _, l := range lines {
		t := userText(l)
		if t == "" || (len(out) > 0 && out[len(out)-1].Text == t) {
			continue
		}
		out = append(out, UserLine{T: l.T, Text: t})
	}
	return out
}
