package appstore

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
)

// The manager of an install travels in the context: the installers are chosen by the kind of an app, and a tool can be made by apt or by pixi.
type managerKey struct{}

// WithManager says which manager makes the app that the context is for ("" is the one of its kind).
func WithManager(ctx context.Context, m string) context.Context {
	return context.WithValue(ctx, managerKey{}, m)
}

func managerOf(ctx context.Context) string { m, _ := ctx.Value(managerKey{}).(string); return m }

// managerOrder is the managers an entry can be installed with, the chosen one first and then the others that are usable, the smallest first
// (the fallback when the chosen one fails). An entry with no options has the one manager it names.
func managerOrder(e Entry) []string {
	first := e.Manager
	if first == "" {
		first = MgrPixi
	}
	order := []string{first}
	var rest []Option
	for _, o := range e.Options {
		if o.Manager != first && o.Err == "" && o.GUI == "" && o.Manager != MgrOurs {
			rest = append(rest, o)
		}
	}
	size := func(o Option) float64 { return o.Size[ArchAMD64] + o.Size[ArchARM64] }
	sort.SliceStable(rest, func(i, j int) bool { return size(rest[i]) < size(rest[j]) })
	for _, o := range rest {
		order = append(order, o.Manager)
	}
	return order
}

// trimmable says whether an install gets the trim: a pixi library is hard-linked from the shared package cache, which keeps its own copy of
// every file, so a trim would free nothing and could only risk the app.
func trimmable(kind, manager string, o Option) bool {
	if o.Full {
		return false
	}
	if manager == MgrPixi && (kind == KindLib || kind == KindPy27) {
		return false
	}
	return true
}

// trimOptionsFor is what the trim of an entry leaves alone: its own keep-list, and the static libraries and headers of a C or C++ library.
func trimOptionsFor(e Entry, o Option) TrimOptions {
	t := TrimOptions{Keep: o.Keep}
	if e.Kind == CKCLib {
		t.KeepStatic = true
		t.Keep = append(t.Keep, "usr/include", "include", ".pixi/envs/default/include")
	}
	return t
}

// installBundle makes the bundle of an app with one manager: install, trim, run the check, and when the check fails after a trim, make it again
// with no trim and say so (the entry is then marked full). It returns what the trim did and whether the bundle is full.
func (s *Service) installBundle(ctx context.Context, a App, manager, dest string, logb *bytes.Buffer) (TrimReport, bool, error) {
	ctx = WithManager(ctx, manager)
	e, haveEntry := s.EntryFor(a.Name, cleanKind(a.Kind))
	o, _ := e.OptionOf(manager)
	if err := s.cfg.Installer.Install(ctx, a.Name, a.Version, a.Kind, dest, logb); err != nil {
		return TrimReport{}, false, err
	}
	var rep TrimReport
	trimmed := haveEntry && trimmable(cleanKind(a.Kind), manager, o)
	if trimmed {
		rep = TrimBundle(dest, trimOptionsFor(e, o))
	}
	err := s.checkApp(ctx, dest, a)
	if err == nil || !trimmed {
		return rep, false, err
	}
	// the app does not run after the trim: make it again whole, and tell the admin which entry needs `full`
	first := err
	os.RemoveAll(dest)
	logb.Reset()
	if err := s.cfg.Installer.Install(ctx, a.Name, a.Version, a.Kind, dest, logb); err != nil {
		return TrimReport{}, false, err
	}
	if err := s.checkApp(ctx, dest, a); err != nil {
		return TrimReport{}, false, err
	}
	s.markFull(e, manager, first)
	return TrimReport{BeforeMB: dirMB(dest), AfterMB: dirMB(dest)}, true, nil
}

// markFull records, on the entry, that the app needs its whole bundle (the trim broke it), and puts the reason on the admin's list.
func (s *Service) markFull(e Entry, manager string, why error) {
	for i := range e.Options {
		if e.Options[i].Manager == manager {
			e.Options[i].Full = true
		}
	}
	if len(e.Options) == 0 {
		e.Options = []Option{{Manager: manager, Full: true}}
	}
	s.AddEntry("system", e)
	s.NoteCheckFailure(e.PackageName(), hostName(), fmt.Sprintf("%s failed its check after the trim (%s): installed whole, and marked full in the catalogue", e.Name, tail(strings.TrimSpace(why.Error()), 200)))
}
