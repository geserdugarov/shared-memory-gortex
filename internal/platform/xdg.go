package platform

import (
	"os"
	"path/filepath"
)

// gortexDir is the application sub-directory Gortex owns inside any of
// the XDG base directories. Every config / data / cache path Gortex
// writes lives under "<base>/gortex/...".
const gortexDir = "gortex"

// xdgBase resolves one XDG base directory. When the named XDG_*_HOME
// environment variable is set AND holds an absolute path, that value
// wins on every platform — this is the "consistent" behaviour: an
// explicit XDG override is always honoured, Linux / macOS / Windows
// alike.
//
// A non-absolute XDG_*_HOME value is ignored, exactly as the XDG Base
// Directory specification mandates ("If [the variable] is set to a
// relative path the value MUST be ignored"). When the variable is
// unset, empty, or relative, the function falls back to
// filepath.Join($HOME, fallbackRel) — the historical Gortex default,
// preserved verbatim so existing installs keep resolving to the same
// location. The optional homeFallback is used only when $HOME itself
// cannot be resolved.
func xdgBase(envVar, fallbackRel, homeFallback string) string {
	if v := os.Getenv(envVar); v != "" && filepath.IsAbs(v) {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return homeFallback
	}
	return filepath.Join(home, fallbackRel)
}

// ConfigHome returns the XDG config base directory: $XDG_CONFIG_HOME
// when set to an absolute path, otherwise $HOME/.config. This is the
// base, not Gortex-scoped — use ConfigDir for the Gortex sub-directory.
func ConfigHome() string {
	return xdgBase("XDG_CONFIG_HOME", ".config", os.TempDir())
}

// DataHome returns the XDG data base directory: $XDG_DATA_HOME when set
// to an absolute path, otherwise $HOME/.local/share.
func DataHome() string {
	return xdgBase("XDG_DATA_HOME", filepath.Join(".local", "share"), os.TempDir())
}

// CacheHome returns the XDG cache base directory: $XDG_CACHE_HOME when
// set to an absolute path, otherwise $HOME/.cache.
//
// Note this deliberately falls back to $HOME/.cache on every platform
// — that is Gortex's historical default and what most subsystems
// (the snapshot store, token cache, daemon state on Unix, …) have
// always used. Subsystems that historically rooted their cache at
// os.UserCacheDir() (which differs from $HOME/.cache on macOS and
// Windows) must call OSCacheHome instead, so their unset-env fallback
// stays byte-identical and existing data is not orphaned.
func CacheHome() string {
	return xdgBase("XDG_CACHE_HOME", ".cache", os.TempDir())
}

// OSCacheHome returns the cache base directory for subsystems whose
// historical default was os.UserCacheDir() rather than $HOME/.cache.
//
// $XDG_CACHE_HOME still wins when set to an absolute path — that is the
// consistency the resolver guarantees, and on Linux os.UserCacheDir()
// already consults XDG_CACHE_HOME anyway. When the variable is unset
// the function falls back to os.UserCacheDir() so the resolved path is
// identical to what these subsystems used before (e.g.
// ~/Library/Caches on macOS, %LocalAppData% on Windows), keeping
// existing on-disk state reachable.
func OSCacheHome() string {
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" && filepath.IsAbs(v) {
		return v
	}
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		return os.TempDir()
	}
	return dir
}

// ConfigDir returns the Gortex configuration directory:
// "<ConfigHome>/gortex". Honours $XDG_CONFIG_HOME; falls back to
// $HOME/.config/gortex when unset.
func ConfigDir() string {
	return filepath.Join(ConfigHome(), gortexDir)
}

// DataDir returns the Gortex data directory: "<DataHome>/gortex".
// Honours $XDG_DATA_HOME; falls back to $HOME/.local/share/gortex when
// unset.
func DataDir() string {
	return filepath.Join(DataHome(), gortexDir)
}

// CacheDir returns the Gortex cache directory: "<CacheHome>/gortex".
// Honours $XDG_CACHE_HOME; falls back to $HOME/.cache/gortex when
// unset.
func CacheDir() string {
	return filepath.Join(CacheHome(), gortexDir)
}

// OSCacheDir returns the Gortex cache directory for subsystems whose
// historical root was os.UserCacheDir(): "<OSCacheHome>/gortex".
// Honours $XDG_CACHE_HOME; falls back to os.UserCacheDir()/gortex when
// unset (preserving the pre-existing macOS / Windows location).
func OSCacheDir() string {
	return filepath.Join(OSCacheHome(), gortexDir)
}

// legacyDir is the dot-directory ($HOME/.gortex) that a few subsystems
// adopted before Gortex grew an XDG-aware layout. It is already the
// Gortex-owned directory (no extra "gortex" sub-directory). New code
// should not add paths here; LegacyConfigDir / LegacyDataDir exist only
// so the pre-XDG subsystems keep an unchanged unset-env fallback.
const legacyDir = ".gortex"

// legacyAwareDir resolves a Gortex directory for a pre-XDG subsystem.
// When the named XDG_*_HOME variable is set to an absolute path it
// wins, and the standard "<base>/gortex" layout is used so the
// subsystem joins the same Gortex tree as everything else. When the
// variable is unset the legacy $HOME/.gortex location is returned
// verbatim, so an existing install's files stay reachable.
func legacyAwareDir(envVar string) string {
	if v := os.Getenv(envVar); v != "" && filepath.IsAbs(v) {
		return filepath.Join(v, gortexDir)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), legacyDir)
	}
	return filepath.Join(home, legacyDir)
}

// LegacyConfigDir returns the Gortex config directory for subsystems
// that historically rooted config-shaped state at $HOME/.gortex
// (rather than $HOME/.config). An absolute $XDG_CONFIG_HOME wins
// ("<XDG_CONFIG_HOME>/gortex"); otherwise the legacy $HOME/.gortex
// location is kept so existing files are not orphaned.
func LegacyConfigDir() string {
	return legacyAwareDir("XDG_CONFIG_HOME")
}

// LegacyDataDir returns the Gortex data directory for subsystems that
// historically rooted data-shaped state at $HOME/.gortex (rather than
// $HOME/.local/share). An absolute $XDG_DATA_HOME wins
// ("<XDG_DATA_HOME>/gortex"); otherwise the legacy $HOME/.gortex
// location is kept so existing files are not orphaned.
func LegacyDataDir() string {
	return legacyAwareDir("XDG_DATA_HOME")
}
