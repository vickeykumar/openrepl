package server

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The colour an admin chose by hand and the colours the colour of the day skips are checked on the server.
func TestSettingsAccentColour(t *testing.T) {
	s, err := SiteSettings{AccentColor: " #008080 ", DisabledColours: []string{"#FF0000", "#ff0000", "#00ff00"}}.normalize()
	if err != nil {
		t.Fatal(err)
	}
	if s.AccentColor != "#008080" || !reflect.DeepEqual(s.DisabledColours, []string{"#00ff00", "#ff0000"}) {
		t.Fatalf("not cleaned: %q %v", s.AccentColor, s.DisabledColours)
	}
	for _, bad := range []string{"red", "#fff", "008080", "#gg0000", "rgb(0,0,0)", "#0080800"} {
		if _, err := (SiteSettings{AccentColor: bad}).normalize(); err == nil || !strings.Contains(err.Error(), "HEX") {
			t.Errorf("accent %q was accepted (%v)", bad, err)
		}
		if _, err := (SiteSettings{DisabledColours: []string{bad}}).normalize(); err == nil {
			t.Errorf("skipped colour %q was accepted", bad)
		}
	}
	// an old settings document has neither field: both stay empty
	old, err := SiteSettings{ColorOfTheDay: true}.normalize()
	if err != nil || old.AccentColor != "" || len(old.DisabledColours) != 0 {
		t.Fatalf("old document: %+v %v", old, err)
	}
}

// Visitors, and workers that follow the gateway, get both fields; a page from before them still reads settings.js.
func TestPublicSettingsColours(t *testing.T) {
	pub := SiteSettings{AccentColor: "#008080", DisabledColours: []string{"#ff0000"}}.public()
	data, _ := json.Marshal(pub)
	if !strings.Contains(string(data), `"accentColor":"#008080"`) || !strings.Contains(string(data), `"disabledColours":["#ff0000"]`) {
		t.Fatalf("public settings: %s", data)
	}
	none, _ := json.Marshal(SiteSettings{}.public())
	if !strings.Contains(string(none), `"disabledColours":[]`) {
		t.Fatalf("an empty list must be [] for the page: %s", none)
	}
	changes := settingsChanges(SiteSettings{}, SiteSettings{AccentColor: "#008080", DisabledColours: []string{"#ff0000"}})
	if len(changes) != 2 {
		t.Fatalf("audit lines: %v", changes)
	}
}
