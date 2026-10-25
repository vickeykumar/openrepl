// Package workspace provides the host identity and ownership policy for REPL
// workspaces. Workspace names must already have been resolved by the server's
// authenticated session or by a verified live JID.
package workspace

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

const (
	DefaultUIDMin uint32 = 1000000000
	DefaultUIDMax uint32 = 2000000000
)

var ErrInvalidHome = errors.New("workspace: invalid home directory")
var ErrIdentityUnavailable = errors.New("workspace: distinct runtime UID/GID switching is unavailable")

var preparedHomes sync.Map
var identityMu sync.Mutex
var identitiesByUID = map[uint32]string{}

// Identity is the stable Linux identity assigned to a workspace. UID and GID
// are derived from the canonical workspace key, so gateway and worker nodes
// produce the same mapping without trusting a value supplied by a client.
type Identity struct {
	Key string
	UID uint32
	GID uint32
}

// IdentityFor resolves a direct child of base to a deterministic runtime ID.
// Operators may set OPENREPL_RUNTIME_UID_MIN and OPENREPL_RUNTIME_UID_MAX to
// select a reserved range shared by all nodes.
func IdentityFor(base, home string) (Identity, error) {
	base, err := filepath.Abs(filepath.Clean(base))
	if err != nil {
		return Identity{}, err
	}
	home, err = filepath.Abs(filepath.Clean(home))
	if err != nil {
		return Identity{}, err
	}
	rel, err := filepath.Rel(base, home)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || strings.ContainsRune(rel, filepath.Separator) {
		return Identity{}, ErrInvalidHome
	}
	if rel == "" || strings.ContainsAny(rel, "\x00/\\") {
		return Identity{}, ErrInvalidHome
	}
	min, err := rangeValue("OPENREPL_RUNTIME_UID_MIN", DefaultUIDMin)
	if err != nil {
		return Identity{}, err
	}
	max, err := rangeValue("OPENREPL_RUNTIME_UID_MAX", DefaultUIDMax)
	if err != nil || max <= min {
		return Identity{}, fmt.Errorf("workspace: invalid runtime UID range")
	}
	key := filepath.ToSlash(rel)
	h := sha256.Sum256([]byte("openrepl-workspace-v1\x00" + key))
	width := uint64(max) - uint64(min) + 1
	id := min + uint32(binary.BigEndian.Uint64(h[:8])%width)
	// Check persisted directory ownership as well as this process's map. This
	// catches a deterministic hash collision after a server restart and fails
	// closed before two existing homes can share one kernel identity.
	entries, readErr := os.ReadDir(base)
	if readErr != nil && !os.IsNotExist(readErr) {
		return Identity{}, readErr
	}
	if readErr == nil {
		for _, entry := range entries {
			if entry.Name() == key || !entry.IsDir() {
				continue
			}
			info, statErr := os.Lstat(filepath.Join(base, entry.Name()))
			if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			st, ok := info.Sys().(*syscall.Stat_t)
			if ok && uint32(st.Uid) == id && uint32(st.Gid) == id {
				return Identity{}, fmt.Errorf("workspace: runtime UID collision with existing home %q", entry.Name())
			}
		}
	}
	identityMu.Lock()
	if prior, exists := identitiesByUID[id]; exists && prior != key {
		identityMu.Unlock()
		return Identity{}, fmt.Errorf("workspace: runtime UID collision between %q and %q; configure a larger reserved range", prior, key)
	}
	identitiesByUID[id] = key
	identityMu.Unlock()
	return Identity{Key: key, UID: id, GID: id}, nil
}

func rangeValue(name string, fallback uint32) (uint32, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("workspace: %s must be a uint32", name)
	}
	return uint32(n), nil
}

// RelativePath accepts the absolute path format used by the browser but
// returns only a clean path below the workspace root.
func RelativePath(home, candidate string) (string, error) {
	home, err := filepath.Abs(filepath.Clean(home))
	if err != nil {
		return "", err
	}
	if candidate == "" {
		return "", nil
	}
	if !filepath.IsAbs(candidate) {
		return "", ErrInvalidHome
	}
	candidate, err = filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(home, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || strings.HasPrefix(rel, string(filepath.Separator)) {
		return "", ErrInvalidHome
	}
	if rel == "." {
		return "", nil
	}
	return filepath.ToSlash(rel), nil
}

// Prepare creates and secures a workspace root and tightens ownership and
// permissions of existing entries. It does not follow symlinks.
func Prepare(base, home string) (Identity, error) {
	id, err := IdentityFor(base, home)
	if err != nil {
		return Identity{}, err
	}
	base, err = filepath.Abs(filepath.Clean(base))
	if err != nil {
		return Identity{}, err
	}
	if err := os.MkdirAll(base, 0711); err != nil {
		return Identity{}, err
	}
	baseInfo, err := os.Lstat(base)
	if err != nil || !baseInfo.IsDir() || baseInfo.Mode()&os.ModeSymlink != 0 {
		return Identity{}, ErrInvalidHome
	}
	if !ownerMatches(baseInfo, uint32(os.Getuid()), uint32(os.Getgid())) {
		if err := os.Chown(base, os.Getuid(), os.Getgid()); err != nil {
			return Identity{}, fmt.Errorf("workspace: set home-root owner: %w", err)
		}
	}
	if baseInfo.Mode().Perm() != 0711 {
		if err := os.Chmod(base, 0711); err != nil {
			return Identity{}, err
		}
	}
	info, err := os.Lstat(home)
	if os.IsNotExist(err) {
		if err = os.Mkdir(home, 0700); err != nil && !os.IsExist(err) {
			return Identity{}, err
		}
		info, err = os.Lstat(home)
	}
	if err != nil {
		return Identity{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Identity{}, ErrInvalidHome
	}
	if !ownerMatches(info, id.UID, id.GID) {
		if err := os.Chown(home, int(id.UID), int(id.GID)); err != nil {
			return Identity{}, fmt.Errorf("workspace: chown root %s: %w", home, err)
		}
	}
	if info.Mode().Perm() != 0700 {
		if err := os.Chmod(home, 0700); err != nil {
			return Identity{}, err
		}
	}
	if _, ok := preparedHomes.Load(id.Key); ok {
		return id, nil
	}
	if err := filepath.Walk(home, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == home {
			return nil
		}
		mode := info.Mode()
		if mode&os.ModeSymlink != 0 {
			if ownerMatches(info, id.UID, id.GID) {
				return nil
			}
			return os.Lchown(path, int(id.UID), int(id.GID))
		}
		if !ownerMatches(info, id.UID, id.GID) {
			if err := os.Chown(path, int(id.UID), int(id.GID)); err != nil {
				return err
			}
		}
		if mode.IsDir() {
			if mode.Perm() != 0700 {
				return os.Chmod(path, 0700)
			}
			return nil
		}
		if mode.IsRegular() {
			// Keep owner execute bits while stripping all group/other access.
			privateMode := (mode.Perm() & 0700) | 0600
			if mode.Perm() != privateMode {
				return os.Chmod(path, privateMode)
			}
			return nil
		}
		// Sockets, FIFOs and devices may be used by code in this home, but
		// ownership still must be private. File APIs skip these types.
		return nil
	}); err != nil {
		return Identity{}, err
	}
	preparedHomes.Store(id.Key, true)
	return id, nil
}

func ownerMatches(info os.FileInfo, uid, gid uint32) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint32(stat.Uid) == uid && uint32(stat.Gid) == gid
}

// ReconcileOwnership reapplies workspace ownership and private modes after a
// privileged server-side or sync operation has written files.
func ReconcileOwnership(base, home string) error {
	id, err := IdentityFor(base, home)
	if err != nil {
		return err
	}
	preparedHomes.Delete(id.Key)
	_, err = Prepare(base, home)
	return err
}

// SwitchingAvailable reports whether the service can start a process with a
// different host UID/GID. Root and CAP_SETUID/CAP_SETGID are both accepted.
func SwitchingAvailable() bool {
	if os.Geteuid() == 0 {
		return true
	}
	return capabilityEffective(7) && capabilityEffective(6)
}

func capabilityEffective(bit uint) bool {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		var mask uint64
		if _, err := fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(line, "CapEff:")), "%x", &mask); err != nil {
			return false
		}
		return mask&(uint64(1)<<bit) != 0
	}
	return false
}

// PrepareCapability reports whether the current service has the capabilities
// needed to secure workspace roots and switch REPL credentials.
func PrepareCapability() error {
	if !SwitchingAvailable() {
		return ErrIdentityUnavailable
	}
	if os.Geteuid() != 0 && !capabilityEffective(0) {
		return fmt.Errorf("workspace: CAP_CHOWN is required to assign workspace ownership")
	}
	if os.Geteuid() != 0 && !capabilityEffective(1) {
		return fmt.Errorf("workspace: CAP_DAC_OVERRIDE is required for service-side workspace operations")
	}
	return nil
}

// Credential returns an exec credential for tools that launch children
// directly. REPLs should normally use the setpriv launcher so no_new_privs
// and the umask are applied before user code starts.
func (i Identity) Credential() *syscall.Credential {
	return &syscall.Credential{Uid: i.UID, Gid: i.GID, Groups: []uint32{}}
}
