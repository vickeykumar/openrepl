package appstore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Live search of the registries behind the mirrors and of the Ubuntu archive (LLD 18 "Search for every kind"): crates.io for Rust crates, Maven
// Central for jars, pkg.go.dev for Go modules, apt-cache for the Debian packages. Each answers with a few results (Found); nothing is
// installed by a search. A registry that does not answer is an error that the page shows as "search not available".

var (
	cratesAPI  = "https://crates.io/api/v1/crates"
	mavenAPI   = "https://search.maven.org/solrsearch/select"
	pkgsiteAPI = "https://pkg.go.dev/v1/search"
	// searchHTTP is the client of the registry searches; the tests replace its transport.
	searchHTTP = &http.Client{Timeout: 12 * time.Second}
)

const searchAgent = "OpenREPL-AppStore/1.0 (+https://openrepl.com)" // crates.io asks every client for one

var (
	crateQuery = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	mavenQuery = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,80}$`)
	goQuery    = regexp.MustCompile(`^[a-z0-9][a-z0-9._/~-]{0,100}$`)
	aptQuery   = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{0,62}$`)
)

// the answers are kept for a while: a page asks again at every key stroke it settles on, and crates.io asks for at most a request a second
var (
	searchMu    sync.Mutex
	searchCache = map[string]cachedFound{}
)

type cachedFound struct {
	at  time.Time
	out []Found
}

const (
	searchTTL      = 10 * time.Minute
	searchCacheMax = 500
	searchLimit    = 20
)

func cachedSearch(key string, fn func() ([]Found, error)) ([]Found, error) {
	searchMu.Lock()
	if c, ok := searchCache[key]; ok && time.Since(c.at) < searchTTL {
		searchMu.Unlock()
		return c.out, nil
	}
	searchMu.Unlock()
	out, err := fn()
	if err != nil {
		return nil, err // an error is not kept
	}
	searchMu.Lock()
	if len(searchCache) >= searchCacheMax {
		searchCache = map[string]cachedFound{}
	}
	searchCache[key] = cachedFound{time.Now(), out}
	searchMu.Unlock()
	return out, nil
}

func getJSON(ctx context.Context, rawurl string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawurl, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", searchAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := searchHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s did not answer: %v", hostOf(rawurl), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d", hostOf(rawurl), resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out)
}

func shortLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n]
	}
	return s
}

// searchCrates asks crates.io.
func searchCrates(ctx context.Context, q string) ([]Found, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if !crateQuery.MatchString(q) {
		return nil, nil
	}
	return cachedSearch("crate:"+q, func() ([]Found, error) {
		var r struct {
			Crates []struct {
				Name        string `json:"name"`
				Newest      string `json:"newest_version"`
				Description string `json:"description"`
			} `json:"crates"`
		}
		if err := getJSON(ctx, fmt.Sprintf("%s?per_page=%d&q=%s", cratesAPI, searchLimit, url.QueryEscape(q)), &r); err != nil {
			return nil, err
		}
		var out []Found
		for _, c := range r.Crates {
			out = append(out, Found{Name: c.Name, Version: c.Newest, Summary: shortLine(c.Description, 160), Kind: KindCrate, Package: c.Name})
		}
		return out, nil
	})
}

// searchMaven asks Maven Central; a result is named group:artifact.
func searchMaven(ctx context.Context, q string) ([]Found, error) {
	q = strings.TrimSpace(q)
	if !mavenQuery.MatchString(q) {
		return nil, nil
	}
	return cachedSearch("jar:"+strings.ToLower(q), func() ([]Found, error) {
		var r struct {
			Response struct {
				Docs []struct {
					ID      string `json:"id"`
					Latest  string `json:"latestVersion"`
					Version string `json:"v"`
				} `json:"docs"`
			} `json:"response"`
		}
		if err := getJSON(ctx, fmt.Sprintf("%s?wt=json&rows=%d&q=%s", mavenAPI, searchLimit, url.QueryEscape(q)), &r); err != nil {
			return nil, err
		}
		var out []Found
		for _, d := range r.Response.Docs {
			v := d.Latest
			if v == "" {
				v = d.Version
			}
			if g, a, ok := strings.Cut(d.ID, ":"); ok && g != "" && a != "" && !strings.Contains(a, ":") {
				out = append(out, Found{Name: strings.ToLower(a), Version: v, Kind: KindJar, Package: d.ID, Summary: g})
			}
		}
		return out, nil
	})
}

// searchGo asks pkg.go.dev (its JSON API, /v1/search); a result is named by its module path.
func searchGo(ctx context.Context, q string) ([]Found, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if !goQuery.MatchString(q) {
		return nil, nil
	}
	return cachedSearch("gomod:"+q, func() ([]Found, error) {
		var r struct {
			Items []struct {
				ModulePath string `json:"modulePath"`
				Version    string `json:"version"`
				Synopsis   string `json:"synopsis"`
			} `json:"items"`
		}
		if err := getJSON(ctx, fmt.Sprintf("%s?limit=%d&q=%s", pkgsiteAPI, searchLimit, url.QueryEscape(q)), &r); err != nil {
			return nil, err
		}
		var out []Found
		seen := map[string]bool{}
		for _, it := range r.Items {
			if it.ModulePath == "" || seen[it.ModulePath] {
				continue
			}
			seen[it.ModulePath] = true
			out = append(out, Found{Name: goShortName(it.ModulePath), Version: it.Version, Summary: shortLine(it.Synopsis, 160), Kind: KindGoMod, Package: it.ModulePath})
		}
		return out, nil
	})
}

// searchApt asks the package lists of the store's own apt state (no root, nothing changed on the system); the lists are downloaded the first time
// an apt package is installed.
func searchApt(ctx context.Context, opts []string, q string, run func(ctx context.Context, name string, args ...string) ([]byte, error)) ([]Found, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if !aptQuery.MatchString(q) {
		return nil, nil
	}
	return cachedSearch("apt:"+q, func() ([]Found, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		args := append(append([]string{}, opts...), "search", "--names-only", q)
		if run == nil {
			run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
				return exec.CommandContext(ctx, name, args...).Output()
			}
		}
		out, err := run(ctx, "apt-cache", args...)
		if err != nil {
			return nil, fmt.Errorf("the Ubuntu package lists could not be searched: %v", err)
		}
		return foundFromAptCache(string(out)), nil
	})
}

// foundFromAptCache reads `apt-cache search`: "name - summary" per line.
func foundFromAptCache(text string) []Found {
	var out []Found
	for _, l := range strings.Split(text, "\n") {
		name, sum, ok := strings.Cut(strings.TrimSpace(l), " - ")
		if !ok || !ValidApt(name) {
			continue
		}
		out = append(out, Found{Name: name, Summary: shortLine(sum, 160), Kind: KindApt})
		if len(out) >= searchLimit {
			break
		}
	}
	return out
}

var majorSuffix = regexp.MustCompile(`^v[0-9]+$`)

// goShortName is the last element of a module path that says something: "cobra" for github.com/spf13/cobra, "uuid" for github.com/x/uuid/v5.
func goShortName(path string) string {
	parts := strings.Split(strings.ToLower(path), "/")
	n := parts[len(parts)-1]
	if majorSuffix.MatchString(n) && len(parts) > 1 {
		n = parts[len(parts)-2]
	}
	return n
}
