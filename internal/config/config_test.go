package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublicRead(t *testing.T) {
	t.Setenv("AARDE_LOG_LEVEL", "info")
	for _, test := range []struct {
		value         string
		want, invalid bool
	}{
		{"", false, false}, {"  ", false, false}, {"false", false, false}, {"0", false, false},
		{"true", true, false}, {"1", true, false}, {" TRUE ", true, false},
		{"yes", false, true}, {"garbage", false, true},
	} {
		t.Run(test.value, func(t *testing.T) {
			t.Setenv("AARDE_WEB_PUBLIC_READ", test.value)
			got, err := Load()
			if (err != nil) != test.invalid || got.WebPublicRead != test.want {
				t.Fatalf("public read: %+v, %v", got, err)
			}
		})
	}
	t.Setenv("AARDE_WEB_PUBLIC_READ", "")
	if err := os.Unsetenv("AARDE_WEB_PUBLIC_READ"); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil || got.WebPublicRead {
		t.Fatalf("unset must be private: %+v %v", got, err)
	}
}

func TestTokenPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("XDG_STATE_HOME", "")
	check := func(c Config, want string) {
		t.Helper()
		got, err := c.TokenPath()
		if err != nil || got != want {
			t.Fatalf("path = %q, %v; want %q", got, err, want)
		}
	}
	check(Config{}, filepath.Join(root, ".local", "state", "aarde", "web.token"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	check(Config{}, filepath.Join(root, "state", "aarde", "web.token"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "runtime"))
	check(Config{}, filepath.Join(root, "runtime", "aarde", "web.token"))
	check(Config{WebTokenPath: filepath.Join(root, "custom.token")}, filepath.Join(root, "custom.token"))
	t.Setenv("AARDE_WEB_TOKEN_PATH", "  relative/web.token  ")
	t.Setenv("AARDE_WEB_PUBLIC_READ", "")
	t.Setenv("AARDE_LOG_LEVEL", "info")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	absolute, err := filepath.Abs("relative/web.token")
	if err != nil {
		t.Fatal(err)
	}
	check(c, absolute)
}

func TestWebBasemap(t *testing.T) {
	t.Setenv("AARDE_LOG_LEVEL", "info")
	t.Setenv("AARDE_WEB_PUBLIC_READ", "false")
	for _, test := range []struct {
		value, want string
		invalid     bool
	}{
		{"", "osm", false}, {"  ", "osm", false}, {"osm", "osm", false},
		{"none", "none", false}, {" none ", "none", false},
		{"off", "", true}, {"invalid", "", true},
	} {
		t.Run(test.value, func(t *testing.T) {
			t.Setenv("AARDE_WEB_BASEMAP", test.value)
			got, err := Load()
			if (err != nil) != test.invalid || (!test.invalid && got.WebBasemap != test.want) {
				t.Fatalf("basemap = %q, %v; want %q, invalid=%t", got.WebBasemap, err, test.want, test.invalid)
			}
		})
	}
}
