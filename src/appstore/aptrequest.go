package appstore

import (
	"fmt"
	"strings"
)

// A request for a Debian package (LLD 18 "Search for every kind"): the name in the request is "apt:<package>", so that it cannot meet an app of
// another kind with the same name; approving it adds the package to the admin's apt list (every server installs it, for every user).

const aptPrefix = "apt:"

func (s *Service) requestApt(who, pkg, reason string) (Request, error) {
	if !ValidApt(pkg) {
		return Request{}, fmt.Errorf("%q is not a package name", pkg)
	}
	if has(s.Apt(), pkg) {
		return Request{}, ErrInstalled
	}
	name := aptPrefix + pkg
	s.mu.Lock()
	defer s.mu.Unlock()
	var r Request
	if !s.get("req:"+name, &r) {
		r = Request{Name: name, Kind: KindApt, At: s.now()}
	}
	if !has(r.Askers, who) {
		day := s.cfg.Now().UTC().Format("2006-01-02")
		var n int
		key := "reqday:" + who + ":" + day
		s.get(key, &n)
		if lim := s.cfg.PerDay(); lim > 0 && n >= lim {
			return Request{}, ErrDaily
		}
		s.put(key, n+1)
	}
	r.Status, r.Answer = "waiting", ""
	r.Askers = addUnique(r.Askers, who)
	if r.Reason == "" {
		r.Reason = clip(reason, 200)
	}
	return r, s.put("req:"+name, r)
}

// approveApt puts the package on the apt list and records the answer for the askers.
func (s *Service) approveApt(admin, name string) error {
	pkg := strings.TrimPrefix(name, aptPrefix)
	if !ValidApt(pkg) {
		return fmt.Errorf("%q is not a package name", pkg)
	}
	if err := s.SetApt(append(s.Apt(), pkg)); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var r Request
	if s.get("req:"+name, &r) {
		r.Status, r.Seen = "approved", nil
		s.put("req:"+name, r)
	}
	return nil
}
