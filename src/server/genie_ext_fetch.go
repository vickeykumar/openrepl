package server

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// Plugins: the package a plugin is, how one is fetched from a link, and how it is put into a workspace.
//
// The server fetches the link; the browser never does. The fetch only goes to public addresses (checked at the moment of the
// connection, so a name that points at a private address is refused too), over https, with a size and a time limit, and only text
// is kept: plugin.json and the .md files of skills/. Anything else in the package is counted and dropped.

const (
	pluginMaxZip     = 2 << 20
	pluginMaxFile    = 32 << 10
	pluginMaxTotal   = 512 << 10
	pluginMaxSkills  = 40
	pluginMaxEntries = 3000
	pluginMaxNotes   = 20
	pluginMaxNote    = 8 << 10
	linkFetchesPerHr = 12
)

type pkgFile struct {
	Path string
	Data []byte
}

// pluginPackage is what a plugin brings, checked.
type pluginPackage struct {
	Manifest pluginManifest
	Files    []pkgFile // plugin.json and skills/<name>.md, sorted by path
	Digest   string
	Source   string // "catalogue:<id>" or "link:<url>"
	Ignored  int
	Skills   []skillInfo
	Notes    []noteInfo
	// the connections the plugin names (genie_conn_requests.go parsePluginConnections)
	Connections []connectionDef
}

// noteInfo is a note a plugin brings: plain Markdown in knowledge/<name>.md that Genie reads when a task is about what it says.
type noteInfo struct {
	Name   string `json:"name"`
	Title  string `json:"title"`
	Plugin string `json:"plugin,omitempty"`
	Path   string `json:"path,omitempty"`
	Source string `json:"source,omitempty"`
	Chars  int    `json:"chars"`
}

var noteNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// noteTitle is the first heading of a note, else its name.
func noteTitle(text, name string) string {
	for _, l := range strings.Split(text, "\n") {
		if t := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "#")); strings.HasPrefix(strings.TrimSpace(l), "#") && t != "" {
			return clipRunes(t, 80)
		}
	}
	return name
}

func digestOf(files []pkgFile) string {
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%d\x00", f.Path, len(f.Data))
		h.Write(f.Data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// buildPackage checks the files of a plugin (paths relative to its folder) and returns the package, or what is wrong. fromLink says
// that it did not come from the catalogue: such a plugin cannot ask for a connection.
func buildPackage(files map[string][]byte, source string, ignored int, fromLink bool) (*pluginPackage, error) {
	mb, ok := files["plugin.json"]
	if !ok {
		return nil, fmt.Errorf("there is no plugin.json at the top of the plugin")
	}
	var m pluginManifest
	dec := json.NewDecoder(bytes.NewReader(mb))
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("plugin.json is not valid JSON: %v", err)
	}
	if !pluginNamePattern.MatchString(m.Name) {
		return nil, fmt.Errorf("the name in plugin.json has to be lower case letters, digits and dashes, starting with a letter")
	}
	if len([]rune(m.Title)) > 60 || len([]rune(m.Description)) > 300 || len(m.Version) > 20 || len([]rune(m.Author)) > 60 {
		return nil, fmt.Errorf("plugin.json has a title, description, version or author that is too long")
	}
	conns, cerr := parsePluginConnections(m.Connections, fromLink)
	if cerr != nil {
		return nil, cerr
	}
	pkg := &pluginPackage{Manifest: m, Source: source, Ignored: ignored, Connections: conns}
	pkg.Files = append(pkg.Files, pkgFile{"plugin.json", mb})
	var paths []string
	for p := range files {
		if p != "plugin.json" {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	names := map[string]bool{}
	for _, p := range paths {
		// a note: plain text, flat in knowledge/
		if strings.HasPrefix(p, "knowledge/") {
			name := strings.TrimSuffix(strings.TrimPrefix(p, "knowledge/"), ".md")
			text := string(files[p])
			if !strings.HasSuffix(p, ".md") || !noteNamePattern.MatchString(name) {
				return nil, fmt.Errorf("%s: a note has to be a file like knowledge/checklist.md, with a name of lower case letters, digits and dashes", p)
			}
			if !utf8.ValidString(text) || strings.ContainsRune(text, 0) || len(text) > pluginMaxNote || strings.TrimSpace(text) == "" {
				return nil, fmt.Errorf("%s: a note has to be text of at most %d KB", p, pluginMaxNote>>10)
			}
			if len(pkg.Notes) >= pluginMaxNotes {
				return nil, fmt.Errorf("the plugin has more than %d notes", pluginMaxNotes)
			}
			pkg.Notes = append(pkg.Notes, noteInfo{Name: name, Title: noteTitle(text, name), Plugin: m.Name, Chars: len(text)})
			pkg.Files = append(pkg.Files, pkgFile{p, files[p]})
			continue
		}
		info, _, err := parseSkill(string(files[p]))
		if err != nil {
			return nil, fmt.Errorf("%s: %v", p, err)
		}
		if p != "skills/"+info.Name+".md" {
			return nil, fmt.Errorf("%s: the file name has to be %s.md, the name in its header", p, info.Name)
		}
		if names[info.Name] {
			return nil, fmt.Errorf("two skills are named %q", info.Name)
		}
		names[info.Name] = true
		info.Source, info.Plugin = "plugin", m.Name
		pkg.Skills = append(pkg.Skills, info)
		pkg.Files = append(pkg.Files, pkgFile{p, files[p]})
	}
	if len(pkg.Skills) == 0 {
		return nil, fmt.Errorf("the plugin has no skills (files like skills/review.md)")
	}
	pkg.Digest = digestOf(pkg.Files)
	return pkg, nil
}

// packageFromZip reads a zip (a GitHub archive has one top folder) and returns the files of the plugin in it: the folder that holds
// plugin.json, inside subpath when one is given.
func packageFromZip(data []byte, subpath, source string) (*pluginPackage, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("that is not a zip file")
	}
	if len(zr.File) > pluginMaxEntries {
		return nil, fmt.Errorf("the zip has too many files")
	}
	type entry struct {
		name string
		f    *zip.File
	}
	var entries []entry
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := f.Name
		if strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") {
			return nil, fmt.Errorf("the zip has a file name that is not allowed")
		}
		for _, seg := range strings.Split(name, "/") {
			if seg == ".." {
				return nil, fmt.Errorf("the zip has a file name that is not allowed")
			}
		}
		entries = append(entries, entry{name, f})
	}
	// the root of the plugin: the shallowest folder that has a plugin.json (inside subpath if one was named)
	subpath = strings.Trim(subpath, "/")
	root, depth, found := "", 1<<30, 0
	for _, e := range entries {
		if path.Base(e.name) != "plugin.json" {
			continue
		}
		dir := strings.Trim(path.Dir(e.name), "/")
		if dir == "." {
			dir = ""
		}
		if subpath != "" {
			// the first folder of a GitHub archive is the repository's: the subpath is under it
			parts := strings.SplitN(dir, "/", 2)
			inner := ""
			if len(parts) == 2 {
				inner = parts[1]
			}
			if inner != subpath {
				continue
			}
		}
		d := 0
		if dir != "" {
			d = strings.Count(dir, "/") + 1
		}
		if d < depth {
			root, depth, found = dir, d, 1
		} else if d == depth {
			found++
		}
	}
	if found == 0 {
		return nil, fmt.Errorf("there is no plugin.json in it")
	}
	if found > 1 {
		return nil, fmt.Errorf("there is more than one plugin.json at the same level: link to one plugin's folder")
	}
	prefix := ""
	if root != "" {
		prefix = root + "/"
	}
	files := map[string][]byte{}
	ignored, total, skills := 0, 0, 0
	for _, e := range entries {
		if !strings.HasPrefix(e.name, prefix) {
			continue
		}
		rel := strings.TrimPrefix(e.name, prefix)
		isNote := strings.HasPrefix(rel, "knowledge/") && strings.HasSuffix(rel, ".md") && strings.Count(rel, "/") == 1
		keep := rel == "plugin.json" || isNote || (strings.HasPrefix(rel, "skills/") && strings.HasSuffix(rel, ".md") && strings.Count(rel, "/") == 1)
		if !keep {
			ignored++
			continue
		}
		if e.f.UncompressedSize64 > pluginMaxFile {
			return nil, fmt.Errorf("%s is longer than %d KB", rel, pluginMaxFile>>10)
		}
		rc, err := e.f.Open()
		if err != nil {
			return nil, fmt.Errorf("%s cannot be read", rel)
		}
		b, err := io.ReadAll(io.LimitReader(rc, pluginMaxFile+1))
		rc.Close()
		if err != nil || len(b) > pluginMaxFile {
			return nil, fmt.Errorf("%s is longer than %d KB", rel, pluginMaxFile>>10)
		}
		total += len(b)
		if total > pluginMaxTotal {
			return nil, fmt.Errorf("the plugin is bigger than %d KB", pluginMaxTotal>>10)
		}
		if rel != "plugin.json" && !isNote {
			skills++
			if skills > pluginMaxSkills {
				return nil, fmt.Errorf("the plugin has more than %d skills", pluginMaxSkills)
			}
		}
		files[rel] = b
	}
	return buildPackage(files, source, ignored, true)
}

// ---- the link --------------------------------------------------------------------------------------------------------------

var githubPart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
var githubRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// parseGitHubLink reads a GitHub link: a repository (https://github.com/owner/repository), or a folder of it
// (https://github.com/owner/repository/tree/<branch>/<folder>). `rest` is what follows "tree": the branch and the folder together, because a branch
// name may hold slashes (feature/x) and then nothing in the link says where the branch ends. ok is false for a link that is not GitHub's.
func parseGitHubLink(raw string) (owner, repo string, rest []string, ok bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 300 {
		return "", "", nil, false, fmt.Errorf("paste a link of at most 300 characters")
	}
	u, perr := url.Parse(raw)
	if perr != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", "", nil, false, fmt.Errorf("the link has to start with https://")
	}
	if p := u.Port(); p != "" && p != "443" {
		return "", "", nil, false, fmt.Errorf("the link cannot name a port")
	}
	host := strings.ToLower(u.Hostname())
	if host != "github.com" && host != "www.github.com" {
		return "", "", nil, false, nil
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segs) < 2 || !githubPart.MatchString(segs[0]) {
		return "", "", nil, true, fmt.Errorf("a GitHub link looks like https://github.com/owner/repository")
	}
	repo = strings.TrimSuffix(segs[1], ".git")
	if !githubPart.MatchString(repo) {
		return "", "", nil, true, fmt.Errorf("a GitHub link looks like https://github.com/owner/repository")
	}
	if len(segs) == 2 {
		return segs[0], repo, nil, true, nil
	}
	if segs[2] != "tree" || len(segs) < 4 {
		return "", "", nil, true, fmt.Errorf("a GitHub link names a repository, or a folder as https://github.com/owner/repository/tree/main/folder")
	}
	rest = segs[3:]
	if len(rest) > 12 {
		return "", "", nil, true, fmt.Errorf("the folder in the link is too deep")
	}
	for _, sg := range rest {
		if sg == "" || sg == "." || sg == ".." || len(sg) > 100 || strings.ContainsAny(sg, "\\\x00") {
			return "", "", nil, true, fmt.Errorf("the branch or the folder in the link is not allowed")
		}
	}
	return segs[0], repo, rest, true, nil
}

// linkSource says where to download a link from: a .zip file at an https address. (A GitHub link does not come here: it is read through
// GitHub's API, githubPackageFiles, so that a folder of a big repository does not need the whole repository.)
func linkSource(raw string) (zipURL, subpath string, err error) {
	if _, _, _, ok, gerr := parseGitHubLink(raw); ok || gerr != nil {
		if gerr != nil {
			return "", "", gerr
		}
		return "", "", fmt.Errorf("a GitHub link is read through the API")
	}
	u, _ := url.Parse(strings.TrimSpace(raw))
	if strings.HasSuffix(strings.ToLower(u.Path), ".zip") {
		return u.String(), "", nil
	}
	return "", "", fmt.Errorf("paste a GitHub repository link or a link to a .zip file")
}

// ---- a GitHub folder, file by file ---------------------------------------------------------------------------------------------------

var githubAPIBase = "https://api.github.com"

// errGitHubRate is GitHub's limit on requests that carry no token (60 an hour for an address). A server that has OPENREPL_GITHUB_TOKEN is
// allowed far more; without one, a repository small enough for a zip is read as a zip instead (githubZipFallback).
var errGitHubRate = fmt.Errorf("GitHub's limit for requests without a token was reached: try again in an hour, or ask an admin to set OPENREPL_GITHUB_TOKEN on the server")

// getURL is the one GET the server makes for a GitHub link; tests replace it. Public addresses only, https, the size limit given.
var getURL = func(rawURL string, limit int64) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "openrepl-plugin-fetch")
	req.Header.Set("Accept", "application/vnd.github+json")
	// the token, if the server has one, goes only to GitHub's own API, never to a download address
	if tok := os.Getenv("OPENREPL_GITHUB_TOKEN"); tok != "" && strings.HasPrefix(rawURL, "https://api.github.com/") {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := newExtClient().Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("GitHub could not be reached")
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("the download stopped")
	}
	if int64(len(b)) > limit {
		return nil, resp.StatusCode, fmt.Errorf("the download is bigger than %d KB", limit>>10)
	}
	return b, resp.StatusCode, nil
}

type githubEntry struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"download_url"`
}

// githubList lists a folder of a repository at a ref ("" is the default branch). found is false when the ref or the folder is not there.
func githubList(owner, repo, ref, path string) (entries []githubEntry, found bool, err error) {
	u := githubAPIBase + "/repos/" + owner + "/" + repo + "/contents"
	if path != "" {
		u += "/" + path
	}
	if ref != "" {
		u += "?ref=" + url.QueryEscape(ref)
	}
	b, status, err := getURL(u, 256<<10)
	if err != nil {
		return nil, false, err
	}
	switch {
	case status == http.StatusNotFound:
		return nil, false, nil
	case status == http.StatusForbidden || status == http.StatusTooManyRequests:
		return nil, false, errGitHubRate
	case status != http.StatusOK:
		return nil, false, fmt.Errorf("GitHub answered %d", status)
	}
	if json.Unmarshal(b, &entries) != nil {
		return nil, false, nil // a file, not a folder
	}
	return entries, true, nil
}

// githubPackageFiles reads the files of a plugin from a GitHub folder: plugin.json, skills/*.md and knowledge/*.md, and counts what else is there.
// The folder may be the repository itself or a folder named in a tree link. The branch of a tree link may hold slashes (feature/x): the link does not say
// where it ends, so each way of cutting it is tried, and the one that is a folder with a plugin.json in it is taken (a name that does not exist answers 404).
func githubPackageFiles(owner, repo string, rest []string) (map[string][]byte, int, error) {
	type cut struct{ ref, path string }
	var cuts []cut
	if len(rest) == 0 {
		cuts = []cut{{"", ""}}
	} else {
		for k := 1; k <= len(rest) && k <= 6; k++ {
			cuts = append(cuts, cut{strings.Join(rest[:k], "/"), strings.Join(rest[k:], "/")})
		}
	}
	for _, c := range cuts {
		entries, found, err := githubList(owner, repo, c.ref, c.path)
		if err != nil {
			return nil, 0, err
		}
		if !found {
			continue
		}
		hasManifest := false
		for _, e := range entries {
			hasManifest = hasManifest || (e.Name == "plugin.json" && e.Type == "file")
		}
		if !hasManifest {
			if len(cuts) == 1 {
				return nil, 0, fmt.Errorf("there is no plugin.json in that folder: link to the folder that holds it")
			}
			continue
		}
		return githubReadPlugin(owner, repo, c.ref, c.path, entries)
	}
	return nil, 0, fmt.Errorf("there is no plugin.json at that link: link to a repository or a folder that holds one (on a branch that exists)")
}

func githubReadPlugin(owner, repo, ref, path string, root []githubEntry) (map[string][]byte, int, error) {
	files := map[string][]byte{}
	ignored, total, skills, notes := 0, 0, 0, 0
	fetchFile := func(rel string, e githubEntry) error {
		if e.Size > pluginMaxFile {
			return fmt.Errorf("%s is longer than %d KB", rel, pluginMaxFile>>10)
		}
		dl := e.DownloadURL
		if !strings.HasPrefix(dl, "https://raw.githubusercontent.com/") && !(extAllowLoopback && strings.HasPrefix(dl, "http://")) {
			return fmt.Errorf("%s has no download address GitHub gave", rel)
		}
		b, status, err := getURL(dl, pluginMaxFile)
		if err != nil || status != http.StatusOK {
			return fmt.Errorf("%s could not be read", rel)
		}
		total += len(b)
		if total > pluginMaxTotal {
			return fmt.Errorf("the plugin is bigger than %d KB", pluginMaxTotal>>10)
		}
		files[rel] = b
		return nil
	}
	for _, e := range root {
		switch {
		case e.Name == "plugin.json" && e.Type == "file":
			if err := fetchFile("plugin.json", e); err != nil {
				return nil, 0, err
			}
		case (e.Name == "skills" || e.Name == "knowledge") && e.Type == "dir":
			sub := e.Name
			if path != "" {
				sub = path + "/" + e.Name
			}
			list, found, err := githubList(owner, repo, ref, sub)
			if err != nil {
				return nil, 0, err
			}
			if !found {
				continue
			}
			for _, f := range list {
				if f.Type != "file" || !strings.HasSuffix(f.Name, ".md") {
					ignored++
					continue
				}
				if e.Name == "skills" {
					skills++
					if skills > pluginMaxSkills {
						return nil, 0, fmt.Errorf("the plugin has more than %d skills", pluginMaxSkills)
					}
				} else {
					notes++
					if notes > pluginMaxNotes {
						return nil, 0, fmt.Errorf("the plugin has more than %d notes", pluginMaxNotes)
					}
				}
				if err := fetchFile(e.Name+"/"+f.Name, f); err != nil {
					return nil, 0, err
				}
			}
		default:
			ignored++
		}
	}
	return files, ignored, nil
}

// githubZipFallback reads the plugin from the zip of the repository when the API's limit was reached: only a repository small enough for the 2 MB limit
// can be read this way. The branch of a tree link may hold slashes: each way of cutting it is tried, and a zip that is not there answers 404.
func githubZipFallback(owner, repo string, rest []string, source string) (*pluginPackage, error) {
	type cut struct{ ref, sub string }
	cuts := []cut{{"HEAD", ""}}
	if len(rest) > 0 {
		cuts = nil
		for k := 1; k <= len(rest) && k <= 6; k++ {
			cuts = append(cuts, cut{strings.Join(rest[:k], "/"), strings.Join(rest[k:], "/")})
		}
	}
	var last error
	for _, c := range cuts {
		data, err := fetchZip("https://codeload.github.com/" + owner + "/" + repo + "/zip/" + c.ref)
		if err != nil {
			last = err
			if strings.Contains(err.Error(), "bigger than") {
				return nil, fmt.Errorf("%v. GitHub's limit for requests without a token was reached too, so this repository cannot be read just now: try again in an hour, or ask an admin to set OPENREPL_GITHUB_TOKEN on the server", err)
			}
			continue
		}
		return packageFromZip(data, c.sub, source)
	}
	if last == nil {
		last = errGitHubRate
	}
	return nil, fmt.Errorf("%v (the branch or the folder in the link may not exist)", last)
}

// githubFiles reads a plugin from a GitHub link; tests replace it.
var githubFiles = githubPackageFiles

// publicIP says whether an address is one a plugin link may be fetched from: not this machine, not a private or link-local network
// (which includes the addresses clouds use for their metadata service), not a shared or reserved range.
func publicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
		switch {
		case v4[0] == 0, v4[0] == 100 && v4[1]&0xC0 == 64, v4[0] == 192 && v4[1] == 0 && v4[2] == 0, v4[0] == 198 && (v4[1] == 18 || v4[1] == 19), v4[0] >= 240:
			return false
		}
	}
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast())
}

// extAllowLoopback lets a test fetch from a server on this machine; nothing else sets it.
var extAllowLoopback = false

func newExtClient() *http.Client { return newExtClientFor(12 * time.Second) }

// newExtClientFor is the client of every call the server makes for a person to an address the person (or a plugin) named: public addresses only, https, a few redirects.
func newExtClientFor(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil || (!extAllowLoopback && !publicIP(ip)) {
			return fmt.Errorf("that address is not a public one")
		}
		return nil
	}}
	tr := &http.Transport{DialContext: dialer.DialContext, Proxy: nil, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 8 * time.Second, DisableKeepAlives: true}
	return &http.Client{Transport: tr, Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return fmt.Errorf("too many redirects")
		}
		if req.URL.Scheme != "https" && !extAllowLoopback {
			return fmt.Errorf("a redirect to a link that is not https")
		}
		return nil
	}}
}

// fetchZip downloads a zip: the one place the server makes the request. Tests replace it.
var fetchZip = func(zipURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, zipURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "openrepl-plugin-fetch")
	resp, err := newExtClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("the link could not be reached")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the link answered %d", resp.StatusCode)
	}
	if resp.ContentLength > pluginMaxZip {
		return nil, fmt.Errorf("the download is bigger than %d MB", pluginMaxZip>>20)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, pluginMaxZip+1))
	if err != nil {
		return nil, fmt.Errorf("the download stopped")
	}
	if len(b) > pluginMaxZip {
		return nil, fmt.Errorf("the download is bigger than %d MB", pluginMaxZip>>20)
	}
	return b, nil
}

func packageFromLink(link string) (*pluginPackage, error) {
	if owner, repo, rest, ok, err := parseGitHubLink(link); err != nil {
		return nil, err
	} else if ok {
		files, ignored, err := githubFiles(owner, repo, rest)
		if err == errGitHubRate {
			return githubZipFallback(owner, repo, rest, "link:"+strings.TrimSpace(link))
		}
		if err != nil {
			return nil, err
		}
		return buildPackage(files, "link:"+strings.TrimSpace(link), ignored, true)
	}
	zipURL, sub, err := linkSource(link)
	if err != nil {
		return nil, err
	}
	data, err := fetchZip(zipURL)
	if err != nil {
		return nil, err
	}
	return packageFromZip(data, sub, "link:"+strings.TrimSpace(link))
}

func packageFromCatalogue(c cataloguePlugin) (*pluginPackage, error) {
	files := map[string][]byte{}
	mb, _ := json.MarshalIndent(c.manifest(), "", "  ")
	files["plugin.json"] = mb
	for _, s := range c.Skills {
		info, _, err := parseSkill(s)
		if err != nil {
			return nil, err
		}
		files["skills/"+info.Name+".md"] = []byte(s)
	}
	for _, n := range c.Notes {
		files["knowledge/"+n.Name+".md"] = []byte(n.Text)
	}
	return buildPackage(files, "catalogue:"+c.Name, 0, false)
}

// ---- links per hour -------------------------------------------------------------------------------------------------------

func linkAllowed(who string, now time.Time) bool {
	return extAllowed("link:"+who, now) && linkHourAllowed(who, now)
}

var linkHours = map[string][]time.Time{}

func linkHourAllowed(who string, now time.Time) bool {
	extRate.Lock()
	defer extRate.Unlock()
	cut := now.Add(-time.Hour)
	keep := linkHours[who][:0]
	for _, t := range linkHours[who] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= linkFetchesPerHr {
		linkHours[who] = keep
		return false
	}
	linkHours[who] = append(keep, now)
	return true
}

// ---- the operations ---------------------------------------------------------------------------------------------------------

type fileChange struct {
	Path   string `json:"path"`
	Status string `json:"status"` // added, changed, removed
}

// changesAgainst says what installing the package would do to the folder of the plugin that is there.
func changesAgainst(dir string, pkg *pluginPackage) []fileChange {
	var out []fileChange
	have := map[string]bool{}
	for _, f := range pkg.Files {
		have[f.Path] = true
		old, ok := plainFile(filepath.Join(dir, filepath.FromSlash(f.Path)), pluginMaxFile+1)
		switch {
		case !ok:
			out = append(out, fileChange{f.Path, "added"})
		case !bytes.Equal(old, f.Data):
			out = append(out, fileChange{f.Path, "changed"})
		}
	}
	for _, sub := range []string{"skills", "knowledge"} {
		if entries, err := os.ReadDir(filepath.Join(dir, sub)); err == nil {
			for _, e := range entries {
				if p := sub + "/" + e.Name(); !have[p] && strings.HasSuffix(e.Name(), ".md") {
					out = append(out, fileChange{p, "removed"})
				}
			}
		}
	}
	return out
}

func (server *Server) handlePluginOp(rw http.ResponseWriter, in extRequest, realHome string, g GenieSettings, who string, reply func(interface{})) {
	_, pluginsDir, err := extDirs(realHome, false)
	if err != nil {
		jsonError(rw, http.StatusInternalServerError, "folder", "The plugins folder could not be used.")
		return
	}
	switch in.Op {
	case "catalogue":
		type row struct {
			ID               string   `json:"id"`
			Title            string   `json:"title"`
			Version          string   `json:"version"`
			Description      string   `json:"description"`
			Official         bool     `json:"official"`
			Skills           []string `json:"skills"`
			Notes            int      `json:"notes"`
			Connections      []string `json:"connections"`
			Installed        bool     `json:"installed"`
			InstalledVersion string   `json:"installedVersion,omitempty"`
			Update           bool     `json:"update"`
			Site             bool     `json:"site"`
		}
		site := map[string]bool{}
		for _, id := range g.SitePlugins {
			site[id] = true
		}
		rows := []row{}
		for _, c := range pluginCatalogue {
			r := row{ID: c.Name, Title: c.Title, Version: c.Version, Description: c.Description, Official: c.Official, Site: site[c.Name], Skills: []string{}}
			for _, s := range c.skillInfos("plugin") {
				r.Skills = append(r.Skills, s.Name)
			}
			r.Notes = len(c.Notes)
			r.Connections = append([]string{}, c.Connections...)
			if p, ok := readInstalled(pluginsDir, c.Name); ok && strings.HasPrefix(p.Source, "catalogue:") {
				r.Installed, r.InstalledVersion = true, p.Version
				r.Update = p.Version != c.Version
			}
			rows = append(rows, r)
		}
		_, installed, _ := workspaceSkills(realHome)
		if installed == nil {
			installed = []installedPlugin{}
		}
		reply(map[string]interface{}{"plugins": rows, "installed": installed, "fromLinks": g.PluginsFromLinks})
		return
	case "plugin_remove":
		if !pluginNamePattern.MatchString(in.Name) {
			jsonError(rw, http.StatusBadRequest, "bad_name", "That is not a plugin name.")
			return
		}
		dir := filepath.Join(pluginsDir, in.Name)
		if st, err := os.Lstat(dir); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			jsonError(rw, http.StatusNotFound, "no_plugin", "That plugin is not installed.")
			return
		}
		old, _ := readInstalled(pluginsDir, in.Name)
		if err := os.RemoveAll(dir); err != nil {
			jsonError(rw, http.StatusInternalServerError, "remove", "The plugin could not be removed.")
			return
		}
		logPluginInstall(pluginInstallEntry{User: who, Op: "remove", Plugin: in.Name, Version: old.Version, Source: old.Source}, time.Now())
		reply(map[string]interface{}{"removed": in.Name})
		return
	}
	// plugin_fetch and plugin_apply: find the package
	var pkg *pluginPackage
	var perr error
	fromLink := false
	switch {
	case in.ID != "":
		c, ok := catalogueByID(in.ID)
		if !ok {
			jsonError(rw, http.StatusNotFound, "no_plugin", "The catalogue has no such plugin.")
			return
		}
		pkg, perr = packageFromCatalogue(c)
	case in.URL != "":
		fromLink = true
	case in.Name != "":
		if !pluginNamePattern.MatchString(in.Name) {
			jsonError(rw, http.StatusBadRequest, "bad_name", "That is not a plugin name.")
			return
		}
		p, ok := readInstalled(pluginsDir, in.Name)
		if !ok {
			jsonError(rw, http.StatusNotFound, "no_plugin", "That plugin is not installed.")
			return
		}
		if strings.HasPrefix(p.Source, "catalogue:") {
			c, ok := catalogueByID(strings.TrimPrefix(p.Source, "catalogue:"))
			if !ok {
				jsonError(rw, http.StatusNotFound, "no_plugin", "The catalogue no longer has this plugin.")
				return
			}
			pkg, perr = packageFromCatalogue(c)
		} else if strings.HasPrefix(p.Source, "link:") {
			in.URL = strings.TrimPrefix(p.Source, "link:")
			fromLink = true
		} else {
			jsonError(rw, http.StatusBadRequest, "no_source", "This plugin was not installed from the catalogue or from a link, so there is nothing to update it from.")
			return
		}
	default:
		jsonError(rw, http.StatusBadRequest, "bad_request", "Say which plugin: an id of the catalogue, a link, or the name of an installed one.")
		return
	}
	if fromLink {
		if !g.PluginsFromLinks {
			jsonError(rw, http.StatusForbidden, "links_off", "Plugins from a link are switched off on this site. Ask an admin to allow them, or choose one from the catalogue.")
			return
		}
		if !linkAllowed(who, time.Now()) {
			jsonError(rw, http.StatusTooManyRequests, "rate", fmt.Sprintf("At most %d links an hour: try again later.", linkFetchesPerHr))
			return
		}
		pkg, perr = packageFromLink(in.URL)
		if perr == nil {
			logExt("link fetched by %s: %s (%d skills, digest %s)", who, in.URL, len(pkg.Skills), pkg.Digest[:12])
		}
	}
	if perr != nil {
		jsonError(rw, http.StatusBadRequest, "bad_plugin", "This plugin cannot be installed: "+perr.Error()+".")
		return
	}
	dir := filepath.Join(pluginsDir, pkg.Manifest.Name)
	cur, installed := readInstalled(pluginsDir, pkg.Manifest.Name)
	access := connAccess(g, who, pkg, fromLink)
	if in.Op == "plugin_request" {
		if !fromLink || (access != "needed" && access != "declined" && access != "waiting") {
			jsonError(rw, http.StatusBadRequest, "no_request", "There is nothing to ask an admin for here.")
			return
		}
		rq, err := createConnRequest(who, pkg, in.Note)
		if err != nil {
			jsonError(rw, http.StatusBadRequest, "request", err.Error()+".")
			return
		}
		logExt("connection request %s by %s: %s from %s", rq.ID, who, pkg.Manifest.Name, pkg.Source)
		reply(map[string]interface{}{"request": rq})
		return
	}
	if in.Op == "plugin_fetch" {
		var changes []fileChange
		if installed {
			changes = changesAgainst(dir, pkg)
		}
		if changes == nil {
			changes = []fileChange{}
		}
		files := []map[string]interface{}{}
		for _, f := range pkg.Files {
			files = append(files, map[string]interface{}{"path": f.Path, "bytes": len(f.Data), "text": string(f.Data)})
		}
		reply(map[string]interface{}{"plugin": map[string]interface{}{
			"name": pkg.Manifest.Name, "title": orStr(pkg.Manifest.Title, pkg.Manifest.Name), "version": pkg.Manifest.Version,
			"description": pkg.Manifest.Description, "author": pkg.Manifest.Author, "source": pkg.Source, "link": fromLink,
			"skills": pkg.Skills, "notes": pkg.Notes, "connections": pkg.Connections, "access": access, "files": files, "ignored": pkg.Ignored, "digest": pkg.Digest,
			"installed": installed, "installedVersion": cur.Version, "changes": changes,
		}})
		return
	}
	// plugin_apply
	if in.Digest == "" || in.Digest != pkg.Digest {
		jsonError(rw, http.StatusConflict, "changed", "It is not the same as what you were shown. Look at it again before you install it.")
		return
	}
	switch access {
	case "off":
		jsonError(rw, http.StatusForbidden, "connections_off", "Connections are switched off on this site, so a plugin that needs one cannot be installed.")
		return
	case "host_denied":
		jsonError(rw, http.StatusForbidden, "host_not_allowed", "The admin's list of servers does not include a server this plugin's connection talks to.")
		return
	case "needed", "waiting", "declined":
		jsonError(rw, http.StatusForbidden, "needs_approval", "This plugin needs a connection to an outside service, and an admin has to approve it first.")
		return
	}
	if !installed {
		_, list, _ := workspaceSkills(realHome)
		if len(list) >= maxPluginsInstall {
			jsonError(rw, http.StatusBadRequest, "too_many", fmt.Sprintf("A workspace keeps at most %d plugins. Remove one first.", maxPluginsInstall))
			return
		}
	}
	if _, _, err := extDirs(realHome, true); err != nil {
		jsonError(rw, http.StatusInternalServerError, "folder", "The plugins folder could not be used.")
		return
	}
	if err := writePlugin(pluginsDir, pkg); err != nil {
		jsonError(rw, http.StatusInternalServerError, "write", "The plugin could not be written.")
		return
	}
	op := "install"
	if installed {
		op = "update"
	}
	logPluginInstall(pluginInstallEntry{User: who, Op: op, Plugin: pkg.Manifest.Name, Version: pkg.Manifest.Version, Source: pkg.Source, Digest: pkg.Digest, Skills: len(pkg.Skills), Notes: len(pkg.Notes)}, time.Now())
	reply(map[string]interface{}{"installed": pkg.Manifest.Name, "version": pkg.Manifest.Version, "skills": len(pkg.Skills)})
}

// writePlugin puts the package in its folder: written to a new folder first, then swapped in, so a failure leaves the old plugin.
func writePlugin(pluginsDir string, pkg *pluginPackage) error {
	final := filepath.Join(pluginsDir, pkg.Manifest.Name)
	tmp := final + ".new"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "skills"), 0o755); err != nil {
		return err
	}
	for _, f := range pkg.Files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(tmp, filepath.FromSlash(f.Path))), 0o755); err != nil {
			os.RemoveAll(tmp)
			return err
		}
		if err := writeTextFile(filepath.Join(tmp, filepath.FromSlash(f.Path)), f.Data); err != nil {
			os.RemoveAll(tmp)
			return err
		}
	}
	rec, _ := json.MarshalIndent(installRecord{Source: pkg.Source, Digest: pkg.Digest, At: time.Now().UTC().Format(time.RFC3339)}, "", "  ")
	if err := writeTextFile(filepath.Join(tmp, "install.json"), rec); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	os.RemoveAll(final)
	if err := os.Rename(tmp, final); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	return nil
}
