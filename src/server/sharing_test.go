package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"trusted"
	"utils"
)

// A collaboration link opens the master's terminal through Firebase, and the collaborator's page
// adds ?homedir=<master's workspace> to its requests so that the file browser shows the master's
// files (LLD 02). These tests keep what that may and may not do.

func collaboratorRequest(t *testing.T, id trusted.Identity, masterHome string) *http.Request {
	t.Helper()
	r := httptest.NewRequest("GET", "/ws?homedir="+url.QueryEscape(masterHome)+"&hsig="+utils.SignHome(masterHome), nil)
	trusted.Set(r.Header, id)
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	return r
}

func withSecret(t *testing.T) {
	t.Helper()
	utils.SetGeneratedSecret("test-secret")
	t.Cleanup(func() { utils.SetGeneratedSecret("") })
}

func TestACollaboratorsFileBrowserFollowsTheMastersWorkspace(t *testing.T) {
	withSecret(t)
	s := &Server{}
	r := collaboratorRequest(t, trusted.Identity{Guest: "g2", Privilege: utils.GUEST}, "/home/openrepl/master-home")

	if !homeOverridden(r) {
		t.Fatal("a request that names a workspace was not seen as one")
	}
	_, home, _ := s.trustedIdentity(r)
	if home != "/home/openrepl/master-home" {
		t.Errorf("the collaborator's file browser shows %q, not the master's workspace", home)
	}
}

func TestACollaboratorOfAnAdminStaysAGuest(t *testing.T) {
	withSecret(t)
	s := &Server{}
	// the admin's workspace is named, the collaborator is a guest
	r := collaboratorRequest(t, trusted.Identity{Guest: "g2", Privilege: utils.GUEST}, "/home/openrepl/admin-home")
	uid, _, privilege := s.trustedIdentity(r)
	if privilege == utils.ADMIN || uid != "" {
		t.Errorf("naming the admin's workspace made the collaborator %q/%q", uid, privilege)
	}

	// and the launch parameters of the collaborator's own terminal (a fork, or its own REPL) are a guest's:
	// sandbox on, workspace the server decided, whatever the page sends
	decided := map[string]string{utils.UidKey: uid, utils.HOME_DIR_KEY: "/home/openrepl/admin-home", utils.USER_PRIVILEGE_KEY: privilege}
	query, _ := url.ParseQuery("homedir=/home/openrepl/admin-home&usermode=admin")
	params := launchParams(query, decided, map[string]string{utils.USER_PRIVILEGE_KEY: utils.ADMIN})
	if utils.IsUserAdmin(params) {
		t.Error("a collaborator got the admin's unsandboxed REPL")
	}
}

func TestTheAdminKeepsTheAdminRoleWhenACollaboratorJoins(t *testing.T) {
	withSecret(t)
	s := &Server{}
	// the master's own request carries no homedir; the collaborator's does
	r := httptest.NewRequest("GET", "/ws", nil)
	trusted.Set(r.Header, trusted.Identity{UID: "vick", HomeID: "vick-home", Privilege: utils.ADMIN})
	r.ParseForm()
	_, _, privilege := s.trustedIdentity(r)
	if privilege != utils.ADMIN {
		t.Fatalf("the admin lost the role: %q", privilege)
	}
	if homeOverridden(r) {
		t.Error("the master's own request was taken for a collaborator's")
	}
}

func TestAForkLinkIsAlsoAnOverrideOfTheWorkspace(t *testing.T) {
	r := httptest.NewRequest("GET", "/ws?jid=abc", nil)
	r.ParseForm()
	if !homeOverridden(r) {
		t.Error("a fork link does not name the parent's workspace")
	}
}

func TestAStrangerWhoKnowsTheWorkspacePathGetsTheirOwn(t *testing.T) {
	withSecret(t)
	s := &Server{}
	home := "/home/openrepl/master-home"
	for name, target := range map[string]string{
		"no signature":      "/ws?homedir=" + url.QueryEscape(home),
		"a made-up one":     "/ws?homedir=" + url.QueryEscape(home) + "&hsig=abcdef",
		"another path":      "/ws?homedir=" + url.QueryEscape(home+"2") + "&hsig=" + utils.SignHome(home),
	} {
		r := httptest.NewRequest("GET", target, nil)
		trusted.Set(r.Header, trusted.Identity{Guest: "stranger", Privilege: utils.GUEST})
		r.ParseForm()
		if homeOverridden(r) {
			t.Errorf("%s: the request was allowed to name a workspace", name)
		}
		_, got, _ := s.trustedIdentity(r)
		if got == home || got == home+"2" {
			t.Errorf("%s: the stranger got %q", name, got)
		}
	}
}
