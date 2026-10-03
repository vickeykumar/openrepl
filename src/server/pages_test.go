package server

import (
	"html/template"
	"io/ioutil"
	"regexp"
	"strings"
	"testing"

	"user"
)

// The pages visitors see: the profile page, the sign-in dialog and the assets
// the binary must carry.

// ---- profile page ----------------------------------------------------------------

func TestProfilePage(t *testing.T) {
	data, err := Asset("static/profile.html")
	if err != nil {
		t.Skip("profile.html is not built into this binary (run make asset)")
	}
	tmpl, err := template.New("profile").Parse(string(data))
	if err != nil {
		t.Fatal(err)
	}
	render := func(p profilePage) string {
		var b strings.Builder
		if err := tmpl.Execute(&b, p); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	member := render(profilePage{UserProfile: user.UserProfile{Uid: "u1", Name: "Ann Lee", Email: "ann@example.com", PhotoURL: "https://example.com/ann.png"}})
	for _, want := range []string{"Ann Lee", "ann@example.com", `src="https://example.com/ann.png"`, "Log out"} {
		if !strings.Contains(member, want) {
			t.Errorf("the profile lacks %q", want)
		}
	}
	if strings.Contains(member, "Admin") {
		t.Error("a member sees an admin badge or link")
	}

	admin := render(profilePage{UserProfile: user.UserProfile{Name: "Boss", Email: "boss@example.com"}, IsAdmin: true})
	if !strings.Contains(admin, `class="profile-badge"`) || !strings.Contains(admin, `href="./admin"`) {
		t.Error("an admin does not get the badge and the link to the dashboard")
	}

	// the name, email and photo come from outside
	evil := render(profilePage{UserProfile: user.UserProfile{
		Name: `<script>alert(1)</script>`, Email: `"><img src=x onerror=alert(2)>`, PhotoURL: "javascript:alert(3)"}})
	for _, bad := range []string{"<script>alert(1)", "<img src=x", "javascript:alert"} {
		if strings.Contains(evil, bad) {
			t.Errorf("the page contains %q unescaped", bad)
		}
	}
}

// ---- sign-in dialog --------------------------------------------------------------

// The page script finds the sign-in dialog's parts by id. This fails when the
// markup in index.html and the script in 05-auth.js stop matching.
func TestSignInDialogMarkupMatchesThePageScript(t *testing.T) {
	script, err := ioutil.ReadFile("../js/src/page/05-auth.js")
	if err != nil {
		t.Skip("page script not available: ", err)
	}
	page, err := ioutil.ReadFile("../resources/index.html")
	if err != nil {
		t.Skip("index.html not available: ", err)
	}
	ids := map[string]bool{}
	for _, m := range regexp.MustCompile(`getElementById\('([a-z-]+)'\)`).FindAllStringSubmatch(string(script), -1) {
		ids[m[1]] = true
	}
	if len(ids) < 10 {
		t.Fatalf("found only %d ids in the script", len(ids))
	}
	delete(ids, "admin-link") // the script adds this one itself, for admins
	for id := range ids {
		if !strings.Contains(string(page), `id="`+id+`"`) {
			t.Errorf("05-auth.js uses #%s, which index.html does not have", id)
		}
	}
	// the dialog that the styles and the script agree on
	for _, want := range []string{`<dialog class="signin" id="signin-dialog"`, `data-mode="signin"`, `data-step="pick"`, `data-state="loading"`, `data-for="signup"`, `data-for="signin"`} {
		if !strings.Contains(string(page), want) {
			t.Errorf("index.html lacks %s", want)
		}
	}
	// the old layout is gone: nothing may float the container or hide the page
	if strings.Contains(string(script), "hide-tag") || strings.Contains(string(script), "fixed-footer") {
		t.Error("05-auth.js still hides the page while signing in")
	}
}

// ---- embedded assets -------------------------------------------------------------

// A page the binary does not carry answers 500, not 404 (the static file
// system reports a missing asset as an error), and a page without its scripts
// is broken. This lists what the site cannot do without, so a binary built from
// an incomplete bindata fails here instead of in the browser.
func TestTheBinaryCarriesThePagesAndTheirScripts(t *testing.T) {
	names := AssetNames()
	if len(names) == 0 {
		t.Skip("no assets are built into this binary (run make asset)")
	}
	for _, name := range []string{
		"static/index.html", "static/profile.html", "static/admin.html", "static/practice.html", "static/doc.html",
		"static/js/gotty-bundle.js", "static/js/scribbler.js", "static/js/preprocessing.js", "static/js/hterm.js",
		"static/js/chat-widget.js", "static/js/jsconsole.js", "static/js/theme.js", "static/js/admin.js",
		"static/css/scribbler-global.css", "static/css/ui-refresh.css", "static/css/admin.css", "static/css/xterm.css",
	} {
		data, err := Asset(name)
		if err != nil || len(data) < 100 {
			t.Errorf("the binary does not carry %s (%d bytes, %v)", name, len(data), err)
		}
	}
}

// ---- contact link -----------------------------------------------------------------

// "Contact" in the nav and the footer scrolls to the feedback form and focuses
// its first field (focusContactForm in 12-landing.js); the practice page links
// to /#request. This fails when the markup and the script stop agreeing.
func TestContactLinksReachTheFeedbackForm(t *testing.T) {
	index, err := ioutil.ReadFile("../resources/index.html")
	if err != nil {
		t.Skip("index.html not available: ", err)
	}
	page := string(index)
	if n := strings.Count(page, "data-contact"); n < 2 {
		t.Errorf("index.html has %d Contact links (nav and footer), want at least 2", n)
	}
	for _, want := range []string{`id="request"`, `id="feedback-form"`, `id="feedback-name"`, `href="#request"`} {
		if !strings.Contains(page, want) {
			t.Errorf("index.html lacks %s", want)
		}
	}
	script, err := ioutil.ReadFile("../js/src/page/12-landing.js")
	if err != nil {
		t.Skip("page script not available: ", err)
	}
	for _, want := range []string{"function focusContactForm", "[data-contact]", `location.hash === "#request"`, `getElementById("feedback-form")`, `getElementById("feedback-name")`} {
		if !strings.Contains(string(script), want) {
			t.Errorf("12-landing.js lacks %s", want)
		}
	}
	practice, err := ioutil.ReadFile("../resources/practice.html")
	if err != nil {
		t.Skip("practice.html not available: ", err)
	}
	if !strings.Contains(string(practice), `href="/#request"`) {
		t.Error("practice.html has no Contact link to /#request")
	}
}
