package utils

import (
	"os"
	"strings"
	"testing"
)

func TestTheRecaptchaSecretIsReadAsItIsAndNeverShown(t *testing.T) {
	settings(t, nil, nil)
	old, had := os.LookupEnv(EnvRecaptchaSecret)
	t.Cleanup(func() {
		if had {
			os.Setenv(EnvRecaptchaSecret, old)
		} else {
			os.Unsetenv(EnvRecaptchaSecret)
		}
	})

	os.Unsetenv(EnvRecaptchaSecret)
	if got := RecaptchaSecret(); got != "" {
		t.Fatalf("not set, got %q", got)
	}
	if got := configSummary(true); !strings.Contains(got, "recaptcha: not set") {
		t.Errorf("the summary does not say it is off: %s", got)
	}

	os.Setenv(EnvRecaptchaSecret, "  s3cr3t-value  ")
	if got := RecaptchaSecret(); got != "s3cr3t-value" {
		t.Fatalf("the value is read trimmed, got %q", got)
	}
	for _, dev := range []bool{false, true} {
		got := configSummary(dev)
		if strings.Contains(got, "s3cr3t-value") {
			t.Errorf("the secret is in the summary (dev=%v): %s", dev, got)
		}
		if !strings.Contains(got, "recaptcha: contact form is checked on the server") {
			t.Errorf("the summary does not say it is on (dev=%v): %s", dev, got)
		}
	}
}

func TestTheRecaptchaSecretIsNotPassedToTheProgramsUsersRun(t *testing.T) {
	if !IsServerEnv(EnvRecaptchaSecret) {
		t.Fatal(EnvRecaptchaSecret + " would reach a REPL")
	}
	got := ChildEnviron([]string{"PATH=/bin", EnvRecaptchaSecret + "=x", "HOME=/home/guest"})
	for _, kv := range got {
		if strings.HasPrefix(kv, EnvRecaptchaSecret+"=") {
			t.Fatalf("passed on: %v", got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("the other variables stay: %v", got)
	}
}
