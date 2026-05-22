package platform

import (
	"os"
	"path/filepath"
	"testing"
)

// clearXDG unsets every XDG base-directory variable so a test starts
// from a known clean slate; t.Setenv restores the prior value at the
// end of the test.
func clearXDG(t *testing.T) {
	t.Helper()
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(v, "")
	}
}

// TestConfigDir_HonorsXDGConfigHome verifies an absolute $XDG_CONFIG_HOME
// is used verbatim.
func TestConfigDir_HonorsXDGConfigHome(t *testing.T) {
	clearXDG(t)
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	want := filepath.Join(xdg, "gortex")
	if got := ConfigDir(); got != want {
		t.Errorf("ConfigDir() = %s, want %s", got, want)
	}
}

// TestConfigDir_UnsetFallback verifies the env-unset fallback stays at
// the historical $HOME/.config/gortex location so existing installs are
// not orphaned.
func TestConfigDir_UnsetFallback(t *testing.T) {
	clearXDG(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	want := filepath.Join(home, ".config", "gortex")
	if got := ConfigDir(); got != want {
		t.Errorf("ConfigDir() = %s, want %s (unset fallback must match the historical default)", got, want)
	}
}

// TestDataDir_HonorsXDGDataHome verifies an absolute $XDG_DATA_HOME is
// used verbatim.
func TestDataDir_HonorsXDGDataHome(t *testing.T) {
	clearXDG(t)
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)

	want := filepath.Join(xdg, "gortex")
	if got := DataDir(); got != want {
		t.Errorf("DataDir() = %s, want %s", got, want)
	}
}

// TestDataDir_UnsetFallback verifies the env-unset fallback is the XDG
// default $HOME/.local/share/gortex.
func TestDataDir_UnsetFallback(t *testing.T) {
	clearXDG(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	want := filepath.Join(home, ".local", "share", "gortex")
	if got := DataDir(); got != want {
		t.Errorf("DataDir() = %s, want %s", got, want)
	}
}

// TestCacheDir_HonorsXDGCacheHome verifies an absolute $XDG_CACHE_HOME
// is used verbatim.
func TestCacheDir_HonorsXDGCacheHome(t *testing.T) {
	clearXDG(t)
	xdg := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", xdg)

	want := filepath.Join(xdg, "gortex")
	if got := CacheDir(); got != want {
		t.Errorf("CacheDir() = %s, want %s", got, want)
	}
}

// TestCacheDir_UnsetFallback verifies the env-unset fallback stays at
// the historical $HOME/.cache/gortex location.
func TestCacheDir_UnsetFallback(t *testing.T) {
	clearXDG(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	want := filepath.Join(home, ".cache", "gortex")
	if got := CacheDir(); got != want {
		t.Errorf("CacheDir() = %s, want %s (unset fallback must match the historical default)", got, want)
	}
}

// TestNonAbsoluteXDGIgnored verifies a relative XDG_*_HOME value is
// ignored, as the XDG Base Directory specification mandates — the
// resolver falls back to the $HOME default instead.
func TestNonAbsoluteXDGIgnored(t *testing.T) {
	clearXDG(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	cases := []struct {
		name   string
		envVar string
		relVal string
		got    func() string
		want   string
	}{
		{"config", "XDG_CONFIG_HOME", "relative/config", ConfigDir, filepath.Join(home, ".config", "gortex")},
		{"data", "XDG_DATA_HOME", "relative/data", DataDir, filepath.Join(home, ".local", "share", "gortex")},
		{"cache", "XDG_CACHE_HOME", "relative/cache", CacheDir, filepath.Join(home, ".cache", "gortex")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.envVar, tc.relVal)
			if got := tc.got(); got != tc.want {
				t.Errorf("%s with relative %s=%q: got %s, want %s (relative value must be ignored)",
					tc.name, tc.envVar, tc.relVal, got, tc.want)
			}
		})
	}
}

// TestOSCacheDir_HonorsXDGCacheHome verifies the os.UserCacheDir-rooted
// helper still honours an absolute $XDG_CACHE_HOME — the consistency
// guarantee the resolver gives every subsystem.
func TestOSCacheDir_HonorsXDGCacheHome(t *testing.T) {
	clearXDG(t)
	xdg := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", xdg)

	want := filepath.Join(xdg, "gortex")
	if got := OSCacheDir(); got != want {
		t.Errorf("OSCacheDir() = %s, want %s", got, want)
	}
}

// TestOSCacheDir_UnsetFallback verifies that with $XDG_CACHE_HOME unset
// OSCacheDir falls back to os.UserCacheDir()/gortex, byte-identical to
// what the os.UserCacheDir-rooted subsystems used before this change.
func TestOSCacheDir_UnsetFallback(t *testing.T) {
	clearXDG(t)
	base, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("os.UserCacheDir unavailable: %v", err)
	}
	want := filepath.Join(base, "gortex")
	if got := OSCacheDir(); got != want {
		t.Errorf("OSCacheDir() = %s, want %s (unset fallback must match os.UserCacheDir)", got, want)
	}
}

// TestOSCacheDir_NonAbsoluteIgnored verifies a relative $XDG_CACHE_HOME
// is ignored by the os.UserCacheDir-rooted helper too.
func TestOSCacheDir_NonAbsoluteIgnored(t *testing.T) {
	clearXDG(t)
	t.Setenv("XDG_CACHE_HOME", "relative/cache")
	base, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("os.UserCacheDir unavailable: %v", err)
	}
	want := filepath.Join(base, "gortex")
	if got := OSCacheDir(); got != want {
		t.Errorf("OSCacheDir() with relative XDG_CACHE_HOME = %s, want %s", got, want)
	}
}
