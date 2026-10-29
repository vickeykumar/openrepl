package server

import (
	"net/url"
	"testing"

	"utils"
)

func TestTheBrowserCannotNameWhatTheServerDecidesAboutTheUser(t *testing.T) {
	// what the server decided: a guest in its own workspace
	decided := map[string]string{utils.UidKey: "", utils.HOME_DIR_KEY: "/home/openrepl/guest-1", utils.USER_PRIVILEGE_KEY: utils.GUEST}
	// what a hostile browser sends, in the init payload and in the arguments of the connection
	hostilePayload := map[string]string{
		utils.USER_PRIVILEGE_KEY: utils.ADMIN, utils.HOME_DIR_KEY: "/home/openrepl/victim", utils.UidKey: "victim",
		utils.IdeLangKey: "c", utils.CompilerOptionKey: "debug",
	}
	hostileQuery, _ := url.ParseQuery("arg=-xc&usermode=admin&homedir=/home/openrepl/victim&uid=victim&jid=abc")

	got := launchParams(hostileQuery, decided, hostilePayload)

	if utils.IsUserAdmin(got) {
		t.Error("a guest made itself admin: its REPL would run without the sandbox")
	}
	if got.Get(utils.HOME_DIR_KEY) != "/home/openrepl/guest-1" {
		t.Errorf("a guest chose its workspace: %q", got.Get(utils.HOME_DIR_KEY))
	}
	if got.Get(utils.UidKey) != "" {
		t.Errorf("a guest chose its user: %q", got.Get(utils.UidKey))
	}
	// what the browser may send still arrives
	if got.Get(utils.IdeLangKey) != "c" || got.Get(utils.CompilerOptionKey) != "debug" || got.Get("jid") != "abc" || got.Get("arg") != "-xc" {
		t.Errorf("the IDE fields and the arguments were lost: %v", got)
	}
}

func TestAnAdminStaysAnAdminAndTheQueryIsNotChanged(t *testing.T) {
	decided := map[string]string{utils.UidKey: "vick", utils.HOME_DIR_KEY: "/home/openrepl/vick", utils.USER_PRIVILEGE_KEY: utils.ADMIN}
	query := url.Values{"arg": {"-i"}}
	got := launchParams(query, decided, map[string]string{"test": "test"})
	if !utils.IsUserAdmin(got) || got.Get(utils.UidKey) != "vick" {
		t.Errorf("the server's decision was lost: %v", got)
	}
	if len(query) != 1 || query.Get(utils.USER_PRIVILEGE_KEY) != "" {
		t.Errorf("the caller's query was modified: %v", query)
	}
}
