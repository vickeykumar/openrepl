package utils

import (
	"net/url"
	"testing"
)

func withSecret(t *testing.T, s string) {
	t.Helper()
	SetGeneratedSecret(s)
	t.Cleanup(func() { SetGeneratedSecret("") })
}

func TestASharedWorkspaceIsNamedOnlyWithItsSignature(t *testing.T) {
	withSecret(t, "test-secret")
	home := "/home/openrepl/master-home"
	sig := SignHome(home)
	if sig == "" {
		t.Fatal("no signature although the server has a secret")
	}
	if got, named, ok := SharedHome(url.Values{HOME_DIR_KEY: {home}, HomeSigKey: {sig}}); !ok || !named || got != home {
		t.Errorf("a signed workspace was refused: %q %v %v", got, named, ok)
	}
	for name, form := range map[string]url.Values{
		"no signature":         {HOME_DIR_KEY: {home}},
		"a wrong signature":    {HOME_DIR_KEY: {home}, HomeSigKey: {"00"}},
		"another path":         {HOME_DIR_KEY: {"/home/openrepl/other"}, HomeSigKey: {sig}},
		"a path with a suffix": {HOME_DIR_KEY: {home + "/.."}, HomeSigKey: {sig}},
	} {
		if got, named, ok := SharedHome(form); ok || !named || got != "" {
			t.Errorf("%s: accepted %q (%v)", name, got, ok)
		}
	}
	if _, named, ok := SharedHome(url.Values{}); named || ok {
		t.Error("a request that names no workspace was taken for one that does")
	}
}

func TestTheSignatureIsTheSameAfterARestartAndFailsClosedWithoutASecret(t *testing.T) {
	withSecret(t, "stable")
	a := SignHome("/h/x")
	SetGeneratedSecret("stable") // the server restarts and reads the same saved secret
	if SignHome("/h/x") != a {
		t.Error("the signature changed with the same secret")
	}
	SetGeneratedSecret("another")
	if ValidHomeSig("/h/x", a) {
		t.Error("a signature of another secret was accepted")
	}
	SetGeneratedSecret("")
	if SignHome("/h/x") != "" || ValidHomeSig("/h/x", "") {
		t.Error("without a secret every workspace name was accepted")
	}
}
