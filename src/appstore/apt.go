package appstore

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// AptRunner installs Debian packages on one node. The packages an admin approves are for every user: they go into the system itself
// (as root, from the node's own package sources), not into the store, so there is no switch for them.
type AptRunner interface {
	Installed(pkg string) bool
	Install(ctx context.Context, pkg string) error
}

// SystemApt is the real one: dpkg to ask, apt-get to install. It needs root and apt-get; a node that has neither says so.
type SystemApt struct{}

func (SystemApt) Installed(pkg string) bool {
	return exec.Command("dpkg", "-s", pkg).Run() == nil
}

func (SystemApt) Install(ctx context.Context, pkg string) error {
	if !ValidApt(pkg) {
		return fmt.Errorf("%q is not a package name", pkg)
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("this server does not run as root, so it cannot install %s", pkg)
	}
	if _, err := exec.LookPath("apt-get"); err != nil {
		return fmt.Errorf("this server has no apt-get")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, "apt-get", "install", "-y", "--no-install-recommends", pkg)
	c.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("apt-get could not install %s: %s", pkg, tail(strings.TrimSpace(string(out)), 300))
	}
	return nil
}

// applyApt makes the node have the approved packages. A package that is there already is left alone, so a node that keeps its system
// between starts does nothing; a failure is said in the node's state and does not stop the others.
func (s *Service) applyApt(ctx context.Context, list []string, st *NodeState) {
	if s.cfg.Apt == nil || len(list) == 0 {
		return
	}
	for _, p := range list {
		if !ValidApt(p) || s.cfg.Apt.Installed(p) {
			continue
		}
		if err := s.cfg.Apt.Install(ctx, p); err != nil {
			st.Failed["apt:"+p] = tail(err.Error(), 300)
		}
	}
}
