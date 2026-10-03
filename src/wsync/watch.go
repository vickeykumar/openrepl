package wsync

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Watcher reports changes in the homes it was given, so that they can be
// sent to a peer. There is one per node: it watches every directory of every
// attached home, and it is not created per request.
//
// It only says that a path changed. The decision whether that is a real
// change, an echo of one just applied from the peer, or nothing at all is
// made later by comparing the path with the base record.
type Watcher struct {
	base   string
	w      *fsnotify.Watcher
	notify func(home, rel string)
	logf   func(format string, args ...interface{})

	mu    sync.Mutex
	homes map[string]*Home

	closeOnce sync.Once
	done      chan struct{}
	finished  chan struct{}
}

// NewWatcher creates a Watcher over the homes under base. notify is called
// from the watcher's goroutine with the home and the path that changed; a
// home of "" means that events were lost and every home must be reconciled.
// It must not block for long.
func NewWatcher(base string, notify func(home, rel string), logf func(format string, args ...interface{})) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	w := &Watcher{
		base:     filepath.Clean(base),
		w:        fw,
		notify:   notify,
		logf:     logf,
		homes:    make(map[string]*Home),
		done:     make(chan struct{}),
		finished: make(chan struct{}),
	}
	go w.loop()
	return w, nil
}

// AddHome starts watching a home and every directory in it. The home must
// exist.
func (w *Watcher) AddHome(name string) error {
	h, err := OpenHome(w.base, name, false)
	if err != nil {
		return err
	}
	w.mu.Lock()
	if _, ok := w.homes[name]; ok {
		w.mu.Unlock()
		h.Close()
		return nil
	}
	w.homes[name] = h
	w.mu.Unlock()
	return w.addTree(name, h, "")
}

// RemoveHome stops watching a home. The directory watches go away by
// themselves when the directories are removed; the rest are dropped here.
func (w *Watcher) RemoveHome(name string) {
	w.mu.Lock()
	h := w.homes[name]
	delete(w.homes, name)
	w.mu.Unlock()
	if h == nil {
		return
	}
	prefix := filepath.Join(w.base, name)
	for _, p := range w.w.WatchList() {
		if p == prefix || strings.HasPrefix(p, prefix+string(filepath.Separator)) {
			w.w.Remove(p)
		}
	}
	h.Close()
}

// Close stops the watcher.
func (w *Watcher) Close() error {
	var err error
	w.closeOnce.Do(func() {
		close(w.done)
		err = w.w.Close()
		<-w.finished
		w.mu.Lock()
		for _, h := range w.homes {
			h.Close()
		}
		w.homes = nil
		w.mu.Unlock()
	})
	return err
}

func (w *Watcher) home(name string) *Home {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.homes[name]
}

// addTree watches the directory rel and every directory under it, found by
// walking from the home's descriptor so that no link is followed. It reports
// each path it finds, because a file may have been created in a new
// directory before the watch on it existed.
func (w *Watcher) addTree(name string, h *Home, rel string) error {
	abs := filepath.Join(w.base, name, filepath.FromSlash(rel))
	var firstErr error
	if err := w.w.Add(abs); err != nil && !os.IsNotExist(err) {
		firstErr = err
		w.logf("wsync: cannot watch %s: %v", abs, err)
	}
	names, err := h.ListDir(rel)
	if err != nil {
		if os.IsNotExist(err) {
			return firstErr
		}
		return err
	}
	for _, n := range names {
		if isTemp(n) {
			continue
		}
		child := join(rel, n)
		if rel != "" {
			// The directory itself was reported by the event that led here.
			w.notify(name, child)
		}
		e, err := h.Lstat(child)
		if err != nil || e.Type != Dir {
			continue
		}
		if err := w.addTree(name, h, child); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (w *Watcher) loop() {
	defer close(w.finished)
	for {
		select {
		case ev, ok := <-w.w.Events:
			if !ok {
				return
			}
			w.handle(ev)
		case err, ok := <-w.w.Errors:
			if !ok {
				return
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				w.logf("wsync: file events were lost; every home will be reconciled")
			} else {
				w.logf("wsync: watcher error: %v", err)
			}
			// Whatever happened, events may be missing.
			w.notify("", "")
		case <-w.done:
			return
		}
	}
}

func (w *Watcher) handle(ev fsnotify.Event) {
	relAbs, err := filepath.Rel(w.base, ev.Name)
	if err != nil || relAbs == "." || strings.HasPrefix(relAbs, "..") {
		return
	}
	parts := strings.SplitN(filepath.ToSlash(relAbs), "/", 2)
	name := parts[0]
	h := w.home(name)
	if h == nil {
		return
	}
	if len(parts) == 1 {
		// The home directory itself changed (it was removed or renamed).
		if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
			w.notify(name, "")
		}
		return
	}
	rel := parts[1]
	for _, c := range strings.Split(rel, "/") {
		if isTemp(c) {
			return // a file being received by the sync itself
		}
	}
	if ev.Op&fsnotify.Create != 0 {
		if e, err := h.Lstat(rel); err == nil && e.Type == Dir {
			w.addTree(name, h, rel)
		}
	}
	w.notify(name, rel)
}
