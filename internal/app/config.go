package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Config is what the app remembers between runs. It is deliberately
// small: everything else on screen is read from the device or from
// GitHub, and a settings file that caches facts about hardware is a
// settings file that will one day disagree with the hardware.
type Config struct {
	// Source is the music folder the Music card starts with.
	Source string `json:"source,omitempty"`
	// BackupDir is where whole-partition backups go. Empty means
	// flasher.DefaultBackupDir, which is what the CLI uses — the two
	// must agree, or `core flash --from-backup` would look in the
	// wrong place for a backup the app took.
	BackupDir string `json:"backup_dir,omitempty"`
	// Names is the friendly name of each iPod, keyed by the DISK's
	// serial (Device.Serial, the SCSI inquiry string) — not by the
	// volume label and not by the drive letter, because the letter
	// changes with the port and the label is the thing the name is
	// there to replace. `core name` writes the same map, through the
	// same file.
	Names map[string]string `json:"names,omitempty"`
	// Theme is the palette choice: "" (System — Onyx when Windows is
	// set to dark apps, Linen otherwise), "linen" or "onyx".
	Theme Theme `json:"theme,omitempty"`
	// MinArtPx is the Fix tab's low-resolution line: a cover smaller
	// than this on its shorter side is listed for replacing. 0 means
	// librarian.DefaultMinArtPx (600); a negative value turns it off.
	MinArtPx int `json:"min_art_px,omitempty"`
	// Volume is the app's playback level, 0–100; absent is 100. It is the
	// app's own output, not the system's volume, and a mute is not saved.
	Volume *int `json:"volume,omitempty"`
	// ReplayGain is Settings → Playback → "Use ReplayGain"; absent is on.
	ReplayGain *bool `json:"replaygain,omitempty"`

	// The Fix tab's four ticks, which are also Settings → Library. The
	// three that start on are *bool, absent meaning on, like ReplayGain;
	// the multi-disc split starts OFF (it changes the shape of an album
	// rather than repairing it), so a plain bool is already "absent is
	// the default".
	DiscFolders bool  `json:"disc_folders,omitempty"`
	FixNames    *bool `json:"fix_names,omitempty"`
	FixOrganize *bool `json:"fix_organize,omitempty"`
	FixLowRes   *bool `json:"fix_lowres,omitempty"`

	// Window is the window's last size in dp; absent is 980×700, and a
	// size below MinWidth×MinHeight is ignored rather than trusted (a
	// hand-edited 0×0 would otherwise open a window nobody can find).
	// Size only: Gio reports no position, and a position saved on one
	// monitor layout is off-screen on the next.
	Window *WindowSize `json:"window,omitempty"`
	// WindowRemember is "Remember the window size"; absent is on. Off
	// deletes Window and stops saving it.
	WindowRemember *bool `json:"window_remember,omitempty"`
	// LastTab is the tab the window closed on: "music", "fix" or
	// "summary"; absent is music.
	LastTab string `json:"last_tab,omitempty"`
	// LastTabRemember is "Reopen the last tab"; absent is on. Off deletes
	// LastTab and stops saving it.
	LastTabRemember *bool `json:"last_tab_remember,omitempty"`

	// The behaviour keys (settings plan, slice C3). Each one changes how
	// the host works, never what a sync writes to the iPod (rule 5).

	// PlaylistsDir is the folder of .m3u8 playlists a sync copies; empty
	// is <music folder>/Playlists, the syncer's own default.
	PlaylistsDir string `json:"playlists_dir,omitempty"`
	// OnlineArt is "Look for covers on the internet"; absent is on. Off,
	// the art client is nil and only a cover file in the album's folder
	// is offered.
	OnlineArt *bool `json:"online_art,omitempty"`
	// CheckDuplicates is "Check for duplicate albums"; absent is on.
	CheckDuplicates *bool `json:"check_duplicates,omitempty"`
	// VerifySync compares copies by content instead of size and date.
	VerifySync bool `json:"verify_sync,omitempty"`
	// EjectAfterSync ejects the iPod when a sync ends with no error.
	EjectAfterSync bool `json:"eject_after_sync,omitempty"`
	// GenreMap is a JSON artist -> genre file used instead of the
	// built-in map; empty is the built-in one.
	GenreMap string `json:"genre_map,omitempty"`
	// UpdateCheck is when the app asks GitHub by itself: "" (when an iPod
	// connects, once a launch) or "never". The menu's Check for updates
	// works either way.
	UpdateCheck string `json:"update_check,omitempty"`
	// Prereleases lets a GitHub pre-release be offered as an update.
	Prereleases bool `json:"prereleases,omitempty"`
	// KeepBackups is how many fwpart-* backups a flash leaves behind,
	// newest first; 0 keeps them all. apple-* files are never counted
	// and never removed.
	KeepBackups int `json:"keep_backups,omitempty"`
	// LogFile is where the app appends its log when --log is not given.
	// Read at start only.
	LogFile string `json:"log_file,omitempty"`
}

// UpdateCheckNever is update_check's one non-default word.
const UpdateCheckNever = "never"

// OnlineArtOn and CheckDuplicatesOn read the two default-on C3 keys.
func (c Config) OnlineArtOn() bool       { return on(c.OnlineArt) }
func (c Config) CheckDuplicatesOn() bool { return on(c.CheckDuplicates) }

// WindowSize is config.json's "window": {"w": 980, "h": 700}, in dp.
type WindowSize struct {
	W int `json:"w"`
	H int `json:"h"`
}

// The window's size when nothing is remembered.
const (
	DefaultWindowW = 980
	DefaultWindowH = 700
)

// on reads a *bool whose absence means true.
func on(b *bool) bool { return b == nil || *b }

// onPtr is the *bool to store for v when absent means true: nil for true,
// so switching a setting back to its default removes the key rather than
// writing the default out (Rule 1 of the settings plan — a key nobody
// moved away from its default is not in the file).
func onPtr(v bool) *bool {
	if v {
		return nil
	}
	return &v
}

// WindowDp is the size the window opens at: the remembered one when it is
// remembered and at least the minimum, else the default.
func (c Config) WindowDp() (w, h int) {
	if on(c.WindowRemember) && c.Window != nil && c.Window.W >= MinWidth && c.Window.H >= MinHeight {
		return c.Window.W, c.Window.H
	}
	return DefaultWindowW, DefaultWindowH
}

// tabKey is a tab as config.json's last_tab spells it.
func tabKey(t Tab) string {
	switch t {
	case TabLibrary:
		return "fix"
	case TabDetails:
		return "summary"
	}
	return "music"
}

// tabFromKey reads last_tab; anything unknown is Music.
func tabFromKey(s string) Tab {
	switch s {
	case "fix":
		return TabLibrary
	case "summary":
		return TabDetails
	}
	return TabAlbums
}

// Restored is the config "Restore defaults" leaves: every key back to its
// default except the iPods' names (`core name` owns them, and a name is
// not a preference) and the music folder (a fact about where the music
// is, not a preference — restoring it would empty the Music and Fix tabs).
func (c Config) Restored() Config {
	return Config{Names: c.Names, Source: c.Source}
}

// NameFor is the friendly name remembered for one iPod, or "".
func (c Config) NameFor(serial string) string {
	if serial == "" {
		return ""
	}
	return c.Names[serial]
}

// SetName remembers (or, with an empty name, forgets) one iPod's
// friendly name. An empty name deletes the entry rather than storing
// "": a map of serials to empty strings is a file that grows every time
// somebody clears a name.
func (c *Config) SetName(serial, name string) {
	if serial == "" {
		return
	}
	if name == "" {
		delete(c.Names, serial)
		return
	}
	if c.Names == nil {
		c.Names = map[string]string{}
	}
	c.Names[serial] = name
}

// ConfigPath is <UserConfigDir>/core/config.json:
// %AppData%\core\config.json on Windows, ~/.config/core/config.json on
// Linux, ~/Library/Application Support/core/config.json on macOS. The
// same parent directory internal/ghrelease caches downloads in and
// internal/flasher writes backups to.
func ConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("app: locating the user config directory: %w", err)
	}
	return filepath.Join(dir, "core", "config.json"), nil
}

// LoadConfig reads the file. A missing file is not an error — it is the
// first run — and neither is a corrupt one: the app starts with empty
// fields and overwrites it on the next save, because refusing to open
// over a bad JSON file would leave the user with a GUI that cannot be
// used to fix it.
func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, nil
	}
	return c, nil
}

// SaveConfig writes the file, creating its directory. It writes to a
// temp name and renames, so a crash mid-write does not leave a
// half-written file that LoadConfig would silently treat as a first
// run.
func SaveConfig(path string, c Config) error {
	if path == "" {
		return errors.New("app: no config path")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("app: create %s: %w", dir, err)
		}
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
