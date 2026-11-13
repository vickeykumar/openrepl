package homesnap

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"objstore"
)

// The files of a home in the object store (changed 2026-11-13). A snapshot is no longer a new object each time: there is one file
// for each UTC day, <home>-<YYYY-MM-DD>.tar.gz, overwritten by every snapshot of that day, so it holds the end of the day; the
// newest Keep (3) days are kept. The history inside a day is the store's own: a versioned bucket (B2) keeps what an overwrite
// replaces, and the profile page lists it (objstore.ListVersions). The objects of the earlier scheme
// (<generation>-<time>-<writer>.tar.gz) are read as before and removed when they are older than legacyKeep.

const (
	legacyKeep = 3 * 24 * time.Hour
	dayLayout  = "2006-01-02"
)

// object kinds
const (
	kindDay    = "day"
	kindLegacy = "legacy"
)

var legacyName = regexp.MustCompile(`^(\d{10})-(\d{8}T\d{6}Z)-[0-9a-f]+\.tar\.gz$`)

func (s *Snapper) dayKey(home string) string {
	return s.prefix(home) + home + "-" + s.now().UTC().Format(dayLayout) + ".tar.gz"
}

// object is a snapshot object of a home, read from its name and the listing.
type object struct {
	key  string
	kind string
	day  string    // kindDay
	at   time.Time // the time of the last write (the listing), else of the name
	gen  int64     // kindLegacy
	size int64
}

// parseObject says what an object of the home's folder is, or false for latest.json and anything else.
func parseObject(home string, o objstore.Object) (object, bool) {
	name := o.Key[strings.LastIndex(o.Key, "/")+1:]
	out := object{key: o.Key, at: o.LastModified, size: o.Size}
	if m := legacyName.FindStringSubmatch(name); m != nil {
		// base 10: Sscan reads "0000000089" as octal and stops at the 8, which made every generation from 8 up "0"
		out.gen, _ = strconv.ParseInt(m[1], 10, 64)
		t, err := time.Parse(timeLayout, m[2])
		if err != nil {
			return out, false
		}
		out.kind = kindLegacy
		out.at = t // the time of the name is when it was made
		return out, true
	}
	day := regexp.MustCompile(`^` + regexp.QuoteMeta(home) + `-(\d{4}-\d{2}-\d{2})\.tar\.gz$`)
	if m := day.FindStringSubmatch(name); m != nil {
		t, err := time.Parse(dayLayout, m[1])
		if err != nil {
			return out, false
		}
		out.kind, out.day = kindDay, m[1]
		if out.at.IsZero() {
			out.at = t
		}
		return out, true
	}
	return out, false
}

// objects lists what a home has in the bucket.
func (s *Snapper) objects(ctx context.Context, c *objstore.Client, home string) ([]object, error) {
	objs, err := c.List(ctx, s.prefix(home))
	if err != nil {
		return nil, err
	}
	var out []object
	for _, o := range objs {
		if ob, ok := parseObject(home, o); ok {
			out = append(out, ob)
		}
	}
	return out, nil
}

// prune keeps the newest Keep days, and removes the objects of the earlier scheme once
// they are old (and a day's file exists). A listing that fails removes nothing. On a versioned bucket a removed object is
// hidden, and its versions stay until the bucket's own rule removes them.
func (s *Snapper) prune(ctx context.Context, c *objstore.Client, home, protect string) int {
	objs, err := s.objects(ctx, c, home)
	if err != nil {
		return 0
	}
	var days, legacy []object
	for _, o := range objs {
		switch o.kind {
		case kindDay:
			days = append(days, o)
		case kindLegacy:
			legacy = append(legacy, o)
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i].day > days[j].day })
	var drop []string
	for i, o := range days {
		if i >= Keep && o.key != protect {
			drop = append(drop, o.key)
		}
	}
	if len(days) > 0 {
		for _, o := range legacy {
			if s.now().Sub(o.at) > legacyKeep && o.key != protect {
				drop = append(drop, o.key)
			}
		}
	}
	deleted := 0
	for _, k := range drop {
		if c.Delete(ctx, k) == nil {
			deleted++
		}
	}
	return deleted
}

// newestValid is the newest snapshot (a day's file, or one of the earlier scheme) that is whole: its hash, when the object has
// one, must match. Three are tried, the newest first; nil when the home has none.
func (s *Snapper) newestValid(ctx context.Context, c *objstore.Client, home string) ([]byte, Latest, error) {
	objs, err := s.objects(ctx, c, home)
	if err != nil {
		return nil, Latest{}, err
	}
	var cands []object
	for _, o := range objs {
		if o.kind == kindDay || o.kind == kindLegacy {
			cands = append(cands, o)
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].at.After(cands[j].at) })
	if len(cands) > Keep {
		cands = cands[:Keep]
	}
	tried := 0
	var forced *forcedCandidate
	for _, o := range cands {
		tried++
		data, obj, gerr := c.Get(ctx, o.key)
		if gerr != nil {
			log.Printf("homesnap: %s could not be read: %v", o.key, gerr)
			continue
		}
		if obj != nil {
			if want := obj.Meta["sha256"]; want != "" && want != sum(data) {
				log.Printf("homesnap: %s is damaged, looking for an older one", o.key)
				continue
			}
		}
		// the archive must also be a whole snapshot (every file as the manifest says): one that is not is passed over for
		// the next, as a damaged one is; otherwise it would fail every restore of the home and shut the older ones out
		if verr := s.verify(data); verr != nil {
			log.Printf("homesnap: %s does not match its manifest (%v), looking for an older one", o.key, verr)
			if forced == nil {
				forced = &forcedCandidate{data: data, obj: obj, o: o}
			}
			continue
		}
		gen := o.gen
		created := o.at
		if obj != nil {
			if g := obj.Meta["generation"]; g != "" {
				fmt.Sscan(g, &gen)
			}
			if t, terr := time.Parse(time.RFC3339, obj.Meta["created"]); terr == nil {
				created = t
			}
		}
		return data, Latest{Generation: gen, Key: o.key, SHA256: sum(data), Size: int64(len(data)), CreatedAt: created}, nil
	}
	if forced != nil {
		// every snapshot that could be read fails the check against its manifest: the newest of them is used as it is, rather
		// than leave the home without its files
		gen, created := forced.o.gen, forced.o.at
		if forced.obj != nil {
			if g := forced.obj.Meta["generation"]; g != "" {
				fmt.Sscan(g, &gen)
			}
			if t, terr := time.Parse(time.RFC3339, forced.obj.Meta["created"]); terr == nil {
				created = t
			}
		}
		log.Printf("homesnap: no snapshot of %s matches its manifest: restoring the newest, %s, as it is", home, forced.o.key)
		return forced.data, Latest{Generation: gen, Key: forced.o.key, SHA256: sum(forced.data), Size: int64(len(forced.data)), CreatedAt: created, Forced: true}, nil
	}
	if tried > 0 {
		return nil, Latest{}, errors.New("homesnap: none of the newest snapshots could be used")
	}
	return nil, Latest{}, nil
}

type forcedCandidate struct {
	data []byte
	obj  *objstore.Object
	o    object
}

// verify reads an archive as a restore would, into a folder that is removed at once.
func (s *Snapper) verify(archive []byte) error {
	if err := os.MkdirAll(s.Base(), 0777); err != nil {
		return err
	}
	tmp := filepath.Join(s.Base(), fmt.Sprintf(".verify-%d", s.now().UnixNano()))
	defer os.RemoveAll(tmp)
	_, err := Extract(archive, tmp)
	return err
}
