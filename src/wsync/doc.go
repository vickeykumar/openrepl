// Package wsync keeps the home directories of a gateway and the node that
// runs their sessions in step. It works on one home at a time: a home is one
// directory directly under the base directory (utils.HOME_DIR).
//
// The package has no network code and imports nothing from the rest of
// OpenREPL. It is given a base directory and, in the conversation layer, a
// stream, so it can be tested against two temporary directories.
//
// Every decision is made against a Record, the state both sides last agreed
// on, saved on disk. See docs/lld/12-workspace-sync.md.
//
// File operations are Linux-only: they use openat and friends so that a path
// can never be walked through a symbolic link, even while a program in the
// home is swapping directories for links. On other systems Open reports
// ErrUnsupported.
package wsync
