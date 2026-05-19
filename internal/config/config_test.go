package config

import (
	"os"
	"testing"
)

func TestDefaults(t *testing.T) {
	os.Unsetenv("CAPTAIN_CONFIG_KEY")
	os.Unsetenv("CAPTAIN_OPEN_PARTICIPATION")
	c := Load()
	if c.ConfigKey != "" {
		t.Fatalf("ConfigKey default = %q, want empty", c.ConfigKey)
	}
	if c.OpenParticipation {
		t.Fatal("OpenParticipation default should be false")
	}
}

func TestEnvOverride(t *testing.T) {
	os.Setenv("CAPTAIN_CONFIG_KEY", "test-key-32bytes-xxxxxxxxxxxxxxxx")
	os.Setenv("CAPTAIN_OPEN_PARTICIPATION", "true")
	defer os.Unsetenv("CAPTAIN_CONFIG_KEY")
	defer os.Unsetenv("CAPTAIN_OPEN_PARTICIPATION")

	c := Load()
	if c.ConfigKey != "test-key-32bytes-xxxxxxxxxxxxxxxx" {
		t.Fatalf("ConfigKey = %q, want passthrough", c.ConfigKey)
	}
	if !c.OpenParticipation {
		t.Fatal("OpenParticipation should be true when env=true")
	}
}
