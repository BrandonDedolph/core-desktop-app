package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BrandonDedolph/core-desktop-app/internal/flasher"
	"github.com/BrandonDedolph/core-desktop-app/internal/librarian"
	"github.com/spf13/cobra"
)

// `core config` reads and writes the settings file core-app keeps,
// <user config dir>/core/config.json, one known key at a time.
//
// The file belongs to the app as much as to this command: core-app may be
// running while it is edited here, and it holds keys this binary has never
// heard of (whatever the app grows next). So every write goes through the
// same map[string]json.RawMessage round-trip `core name` uses — the key
// being changed is the only one decoded, everything else is carried across
// as it was — and lands by temp-and-rename. internal/app is not imported:
// it would link Gio into `core`.
//
// Absent means default (settings plan, rule 1). `unset` deletes the key
// rather than writing the default's value, and `set` of a word that only
// means "the default" (theme system) does the same, so the file only ever
// holds what somebody chose.

// configKind is what a key's value is, which decides how `set` parses it.
type configKind int

const (
	kindPath   configKind = iota // a folder or file, stored as typed
	kindBool                     // true/false
	kindInt                      // a whole number, range-checked
	kindEnum                     // one of a few words
	kindWindow                   // {w,h}; the window writes it, never this command
)

func (k configKind) String() string {
	switch k {
	case kindPath:
		return "path"
	case kindBool:
		return "true/false"
	case kindInt:
		return "number"
	case kindWindow:
		return "size (saved by the window)"
	}
	return ""
}

// configKey is one row of the settings plan's key table.
type configKey struct {
	Name string
	Kind configKind
	// Desc is the one line `core config --help` prints under the name.
	Desc string
	// Default is what an absent key means, in the words the listing
	// prints. A func because backup_dir's is a path on this machine.
	Default func() string
	// Values are an enum's accepted words. DefaultWord, when set, is the
	// word that means "absent": setting it deletes the key, because the
	// app reads the key's empty value and not that word (theme "" is
	// System; the string "system" is not a theme it knows).
	Values      []string
	DefaultWord string
	// Check is an int's range, returning the refusal in plain words.
	Check func(int) error
	// File: the path names a file, not a folder (the genre map).
	// Created: a file whoever uses it creates, so only its folder has to
	// exist already (the log).
	File, Created bool
}

func constDefault(s string) func() string { return func() string { return s } }

// configKeys is the settings plan's table, minus `names` (core name owns
// it). Keys the app only starts reading in a later slice are here too:
// they are plain JSON, and a file written now is read correctly then.
var configKeys = []configKey{
	{Name: "source", Kind: kindPath, Default: constDefault("(none)"),
		Desc: "the music folder; --src for sync, fix, organize and index when neither the flag nor $CORELIB_SRC says"},
	{Name: "backup_dir", Kind: kindPath, Default: func() string {
		if d, err := flasher.DefaultBackupDir(); err == nil {
			return d
		}
		return "<user config dir>/core/backups"
	}, Desc: "where firmware backups go; --backup-dir for flash, update and install, and where backup writes"},
	{Name: "theme", Kind: kindEnum, Values: []string{"system", "linen", "onyx"}, DefaultWord: "system",
		Default: constDefault("system"), Desc: "the app's palette; system follows Windows' dark-mode setting"},
	{Name: "min_art_px", Kind: kindInt, Default: constDefault(strconv.Itoa(librarian.DefaultMinArtPx)),
		Check: func(n int) error {
			if n == -1 || n >= 100 {
				return nil
			}
			return errors.New("min_art_px is -1 or off, or a size of 100 px or more")
		}, Desc: "a cover smaller than this on its short side is low-resolution, -1 for off; fix --min-art"},
	{Name: "volume", Kind: kindInt, Default: constDefault("100"),
		Check: func(n int) error {
			if n >= 0 && n <= 100 {
				return nil
			}
			return errors.New("volume is 0 to 100")
		}, Desc: "the app's playback volume (its own, not the system's)"},
	{Name: "replaygain", Kind: kindBool, Default: constDefault("true"),
		Desc: "play FLACs at their ReplayGain level, the way the iPod does"},
	{Name: "disc_folders", Kind: kindBool, Default: constDefault("false"),
		Desc: "split multi-disc albums into Disc N folders; --disc-folders for fix and organize"},
	{Name: "fix_names", Kind: kindBool, Default: constDefault("true"),
		Desc: "the Fix tab's Fix all renames files to match their tags"},
	{Name: "fix_organize", Kind: kindBool, Default: constDefault("true"),
		Desc: "the Fix tab's Fix all moves files into their album folders"},
	{Name: "fix_lowres", Kind: kindBool, Default: constDefault("true"),
		Desc: "the Fix tab's Fix all replaces low-resolution covers"},
	{Name: "window", Kind: kindWindow, Default: constDefault("980×700"),
		Desc: "the app's window size; the window saves it, unset forgets it"},
	{Name: "window_remember", Kind: kindBool, Default: constDefault("true"),
		Desc: "remember the window size; off opens at 980×700 every time"},
	{Name: "last_tab", Kind: kindEnum, Values: []string{"music", "fix", "summary"},
		Default: constDefault("music"), Desc: "the tab the app opens on"},
	{Name: "last_tab_remember", Kind: kindBool, Default: constDefault("true"),
		Desc: "reopen the tab the app was closed on"},
	{Name: "playlists_dir", Kind: kindPath, Default: constDefault("<source>/Playlists"),
		Desc: "the folder of .m3u8 playlists a sync copies"},
	{Name: "online_art", Kind: kindBool, Default: constDefault("true"),
		Desc: "look for covers on the internet (iTunes, MusicBrainz + Cover Art Archive)"},
	{Name: "artist_pictures", Kind: kindBool, Default: constDefault("false"),
		Desc: "artist photos from Deezer on the Library tab (needs online_art too); off, a mosaic of the artist's covers"},
	{Name: "music_view", Kind: kindEnum, Values: []string{"albums", "artists", "genres", "songs"}, DefaultWord: "albums",
		Default: constDefault("albums"), Desc: "the Library tab's view: albums, artists, genres or songs"},
	{Name: "check_duplicates", Kind: kindBool, Default: constDefault("true"),
		Desc: "look for albums that are in the music folder twice"},
	{Name: "verify_sync", Kind: kindBool, Default: constDefault("false"),
		Desc: "compare copies by content on sync (slower)"},
	{Name: "eject_after_sync", Kind: kindBool, Default: constDefault("false"),
		Desc: "eject the iPod when a sync finishes"},
	{Name: "genre_map", Kind: kindPath, File: true, Default: constDefault("(built-in)"),
		Desc: "a JSON artist -> genre map file to use instead of the built-in one"},
	{Name: "update_check", Kind: kindEnum, Values: []string{"connect", "never"},
		Default: constDefault("connect"), Desc: "when the app looks for new firmware: when an iPod connects, or never"},
	{Name: "prereleases", Kind: kindBool, Default: constDefault("false"),
		Desc: "include pre-releases when looking for updates"},
	{Name: "keep_backups", Kind: kindInt, Default: constDefault("0 (all)"),
		Check: func(n int) error {
			if n >= 0 {
				return nil
			}
			return errors.New("keep_backups is 0 (keep them all) or how many of the newest to keep")
		}, Desc: "how many firmware backups to keep, 0 for all; the Apple firmware backup is always kept"},
	{Name: "log_file", Kind: kindPath, File: true, Created: true, Default: constDefault("(none)"),
		Desc: "also write the app's log to this file"},
}

func lookupConfigKey(name string) (configKey, bool) {
	for _, k := range configKeys {
		if k.Name == name {
			return k, true
		}
	}
	return configKey{}, false
}

func configKeyNames() string {
	names := make([]string, len(configKeys))
	for i, k := range configKeys {
		names[i] = k.Name
	}
	return strings.Join(names, ", ")
}

// errNamesOwned is the refusal for the one key in the file that is not a
// setting: the iPod names are filed by disk serial, and `core name` is
// the command that reads a serial off the volume.
var errNamesOwned = errors.New("names holds the iPods' friendly names, filed by disk serial; " +
	"use `core name <drive> \"<name>\"` to set one")

func newConfigCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read or change the settings core and core-app share",
		Long: `Reads or changes the settings file, <user config dir>/core/config.json —
the same file core-app's Settings sheet writes.

  core config                  every setting: its value, or (default)
  core config get <key>
  core config set <key> <value>
  core config unset <key>      back to the default
  core config path             where the file is

A flag always beats the file, and $CORELIB_SRC beats "source": the file
is what a command uses when nothing more specific was said, and a
command that took a value from it says so on stderr.

The settings:

` + configKeyHelp() + `
Booleans take true/false, on/off or yes/no. Paths are stored as typed.
The iPod names are in the same file, but "core name" sets them.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigList(cmd.OutOrStdout(), asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the file's whole object, as stored")

	cmd.AddCommand(
		&cobra.Command{
			Use:          "get <key>",
			Short:        "Print one setting",
			Args:         cobra.ExactArgs(1),
			SilenceUsage: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runConfigGet(cmd.OutOrStdout(), args[0])
			},
		},
		&cobra.Command{
			Use:   "set <key> <value>",
			Short: "Change one setting",
			// Flags are not parsed here, because the values include
			// negative numbers: `set min_art_px -1` is how the check is
			// turned off, and to a flag parser -1 is an unknown flag.
			DisableFlagParsing: true,
			SilenceUsage:       true,
			RunE: func(cmd *cobra.Command, args []string) error {
				for _, a := range args {
					if a == "-h" || a == "--help" {
						return cmd.Help()
					}
				}
				if len(args) != 2 {
					return fmt.Errorf("set takes a key and a value (`core config set min_art_px 1000`), got %d argument(s)", len(args))
				}
				return runConfigSet(cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], args[1])
			},
		},
		&cobra.Command{
			Use:          "unset <key>",
			Short:        "Put one setting back to its default",
			Args:         cobra.ExactArgs(1),
			SilenceUsage: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runConfigUnset(cmd.OutOrStdout(), args[0])
			},
		},
		&cobra.Command{
			Use:          "path",
			Short:        "Print where the settings file is",
			Args:         cobra.NoArgs,
			SilenceUsage: true,
			RunE: func(cmd *cobra.Command, args []string) error {
				path, err := nameConfigPath()
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), path)
				return nil
			},
		},
	)
	return cmd
}

func configKeyHelp() string {
	var b strings.Builder
	for _, k := range configKeys {
		kind := k.Kind.String()
		if k.Kind == kindEnum {
			kind = strings.Join(k.Values, " | ")
		}
		fmt.Fprintf(&b, "  %-18s %s, default %s\n  %-18s %s\n", k.Name, kind, k.Default(), "", k.Desc)
	}
	return b.String()
}

// readConfigStrict is readConfigObject without the forgiveness. `core
// name` and the defaults below treat a corrupt file as empty, because
// their real work is elsewhere; this command's work IS the file, and
// writing one key over a file it could not read would throw every other
// setting away.
func readConfigStrict(path string) (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	raw := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(b)) == 0 {
		return raw, nil
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON (%v); fix it by hand, or delete it to start over "+
			"(that also forgets the iPod names)", path, err)
	}
	return raw, nil
}

// storedValue is a key's value as the file holds it, or ok=false when the
// key is absent (a JSON null counts as absent, as it does for the app).
func storedValue(raw map[string]json.RawMessage, name string) (json.RawMessage, bool) {
	v, ok := raw[name]
	if !ok || string(bytes.TrimSpace(v)) == "null" {
		return nil, false
	}
	return v, true
}

// displayValue is a stored value in the listing's words: strings without
// their quotes, everything else as the compact JSON it is.
func displayValue(k configKey, v json.RawMessage) string {
	var s string
	if json.Unmarshal(v, &s) == nil {
		if s == "" && k.DefaultWord != "" {
			return k.DefaultWord
		}
		return s
	}
	var c bytes.Buffer
	if json.Compact(&c, v) == nil {
		return c.String()
	}
	return string(v)
}

func runConfigList(out io.Writer, asJSON bool) error {
	path, err := nameConfigPath()
	if err != nil {
		return err
	}
	raw, err := readConfigStrict(path)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, raw)
	}
	width := 0
	for _, k := range configKeys {
		width = max(width, len(k.Name))
	}
	for _, k := range configKeys {
		if v, ok := storedValue(raw, k.Name); ok {
			fmt.Fprintf(out, "%-*s = %s\n", width, k.Name, displayValue(k, v))
		} else {
			fmt.Fprintf(out, "%-*s = %s (default)\n", width, k.Name, k.Default())
		}
	}
	// What else is in the file is the app's business (or `core name`'s),
	// but a key nobody can see is a key nobody can explain: name them.
	var other []string
	for name := range raw {
		if _, ok := lookupConfigKey(name); !ok {
			other = append(other, name)
		}
	}
	sort.Strings(other)
	if len(other) > 0 {
		fmt.Fprintf(out, "\nalso in the file: %s\n", strings.Join(other, ", "))
	}
	fmt.Fprintf(out, "\n(%s)\n", path)
	return nil
}

// knownKey resolves a name typed on the command line, or says what the
// names are.
func knownKey(name string) (configKey, error) {
	if name == "names" {
		return configKey{}, errNamesOwned
	}
	k, ok := lookupConfigKey(name)
	if !ok {
		return configKey{}, fmt.Errorf("there is no setting called %q; the settings are: %s "+
			"(`core config --help` says what each one is)", name, configKeyNames())
	}
	return k, nil
}

func runConfigGet(out io.Writer, name string) error {
	k, err := knownKey(name)
	if err != nil {
		return err
	}
	path, err := nameConfigPath()
	if err != nil {
		return err
	}
	raw, err := readConfigStrict(path)
	if err != nil {
		return err
	}
	if v, ok := storedValue(raw, k.Name); ok {
		fmt.Fprintln(out, displayValue(k, v))
	} else {
		fmt.Fprintf(out, "%s (default)\n", k.Default())
	}
	return nil
}

// parseConfigValue turns what was typed into the JSON the app reads, or
// refuses it with the values that would have been accepted. A nil result
// with no error means "this is the default": delete the key.
func parseConfigValue(k configKey, value string) (json.RawMessage, error) {
	switch k.Kind {
	case kindWindow:
		return nil, errors.New("window is saved by the app's window when you resize it; " +
			"`core config unset window` forgets it, `core config set window_remember off` stops it")
	case kindPath:
		if strings.TrimSpace(value) == "" {
			return nil, nil
		}
		return json.Marshal(value)
	case kindBool:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "on", "yes":
			return json.RawMessage("true"), nil
		case "false", "off", "no":
			return json.RawMessage("false"), nil
		}
		return nil, fmt.Errorf("%s is true or false (or on/off, yes/no), not %q", k.Name, value)
	case kindInt:
		v := strings.TrimSpace(value)
		if k.Name == "min_art_px" && strings.EqualFold(v, "off") {
			v = "-1" // the word the Settings picker shows
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("%s is a whole number, not %q", k.Name, value)
		}
		if k.Check != nil {
			if err := k.Check(n); err != nil {
				return nil, fmt.Errorf("%w, not %d", err, n)
			}
		}
		return json.RawMessage(strconv.Itoa(n)), nil
	case kindEnum:
		v := strings.ToLower(strings.TrimSpace(value))
		for _, ok := range k.Values {
			if v == ok {
				if v == k.DefaultWord {
					return nil, nil
				}
				return json.Marshal(v)
			}
		}
		return nil, fmt.Errorf("%s is one of %s, not %q", k.Name, strings.Join(k.Values, ", "), value)
	}
	return nil, fmt.Errorf("%s cannot be set here", k.Name)
}

func runConfigSet(out, errw io.Writer, name, value string) error {
	k, err := knownKey(name)
	if err != nil {
		return err
	}
	enc, err := parseConfigValue(k, value)
	if err != nil {
		return err
	}
	path, err := nameConfigPath()
	if err != nil {
		return err
	}
	raw, err := readConfigStrict(path)
	if err != nil {
		return err
	}
	if enc == nil {
		delete(raw, k.Name)
	} else {
		raw[k.Name] = enc
	}
	if err := writeConfigObject(path, raw); err != nil {
		return err
	}
	if enc == nil {
		fmt.Fprintf(out, "%s = %s (default)\n", k.Name, k.Default())
		return nil
	}
	fmt.Fprintf(out, "%s = %s\n", k.Name, displayValue(k, enc))
	if k.Kind == kindPath {
		warnMissingPath(errw, k, value)
	}
	return nil
}

// warnMissingPath is a warning and not a refusal: a music folder on a
// drive that is not plugged in right now is still the right setting.
func warnMissingPath(errw io.Writer, k configKey, value string) {
	check := value
	if k.Created {
		check = filepath.Dir(value)
	}
	fi, err := os.Stat(check)
	switch {
	case err != nil:
		fmt.Fprintf(errw, "warning: %s does not exist (yet); saved anyway\n", check)
	case k.Created:
		// The folder is there; the file is made when it is first written.
	case k.File && fi.IsDir():
		fmt.Fprintf(errw, "warning: %s is a folder, and %s names a file; saved anyway\n", check, k.Name)
	case !k.File && !fi.IsDir():
		fmt.Fprintf(errw, "warning: %s is a file, and %s names a folder; saved anyway\n", check, k.Name)
	}
}

func runConfigUnset(out io.Writer, name string) error {
	k, err := knownKey(name)
	if err != nil {
		return err
	}
	path, err := nameConfigPath()
	if err != nil {
		return err
	}
	raw, err := readConfigStrict(path)
	if err != nil {
		return err
	}
	if _, ok := raw[k.Name]; ok {
		delete(raw, k.Name)
		if err := writeConfigObject(path, raw); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "%s = %s (default)\n", k.Name, k.Default())
	return nil
}

// --- defaults from the file ---------------------------------------------

// The order every command follows: a flag that was typed, then an
// environment variable where one already exists ($CORELIB_SRC), then the
// file, then the built-in default. The file is read best-effort — a
// missing or corrupt config.json is "nothing configured", never a reason
// for a sync to refuse to run — and a value taken from it is said once on
// stderr, so a surprising folder has its explanation on the screen
// (stdout is left alone for --json).

// configDefault reads one key for use as a default. It is false when the
// key is absent, null, or not the JSON type the caller decodes into — a
// mistyped value is ignored the way the app ignores it, not obeyed.
func configDefault(name string, into any) bool {
	path, err := nameConfigPath()
	if err != nil {
		return false
	}
	raw, err := readConfigObject(path)
	if err != nil {
		return false
	}
	v, ok := storedValue(raw, name)
	if !ok {
		return false
	}
	return json.Unmarshal(v, into) == nil
}

func noteFromConfig(cmd *cobra.Command, what, value string) {
	fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s (from core config)\n", what, value)
}

// resolveSrc is --src for sync, fix, organize and index: the flag, then
// $CORELIB_SRC, then the file's "source". "" means none of them said.
func resolveSrc(cmd *cobra.Command, flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("CORELIB_SRC"); env != "" {
		return env
	}
	var s string
	if configDefault("source", &s) && strings.TrimSpace(s) != "" {
		noteFromConfig(cmd, "source", s)
		return s
	}
	return ""
}

// errNoSrc is the refusal every --src command gives, naming all three
// ways to say it.
var errNoSrc = errors.New("--src is required (or set CORELIB_SRC, " +
	"or run `core config set source <music folder>` once)")

// resolveBoolFlag is a boolean flag with a key behind it: the flag when
// it was typed, else the file, else the flag's own default.
func resolveBoolFlag(cmd *cobra.Command, flag, key string, cur bool) bool {
	if cmd.Flags().Changed(flag) {
		return cur
	}
	var b bool
	if !configDefault(key, &b) {
		return cur
	}
	if b != cur {
		noteFromConfig(cmd, "--"+flag, strconv.FormatBool(b))
	}
	return b
}

// resolveMinArt is fix --min-art. The two say "off" differently: the flag
// takes 0 (its default is the real number, so 0 can only be typed), the
// file takes a negative number (0 there is "absent", which is 600).
func resolveMinArt(cmd *cobra.Command, cur int) int {
	if cmd.Flags().Changed("min-art") {
		return cur
	}
	var n int
	if !configDefault("min_art_px", &n) || n == 0 {
		return cur
	}
	if n < 0 {
		noteFromConfig(cmd, "--min-art", "off")
		return 0
	}
	if n != cur {
		noteFromConfig(cmd, "--min-art", strconv.Itoa(n))
	}
	return n
}

// resolveBackupDir is --backup-dir for flash, update and install. The app
// reads the same key for its own backups; if the two disagreed, `core
// flash --from-backup` would be pointed at a folder the backup is not in.
func resolveBackupDir(cmd *cobra.Command, cur string) (dir string, fromFile bool) {
	if cmd.Flags().Changed("backup-dir") {
		return cur, false
	}
	var s string
	if configDefault("backup_dir", &s) && strings.TrimSpace(s) != "" {
		noteFromConfig(cmd, "backup folder", s)
		return s, true
	}
	return cur, false
}

// configuredBackupDir is the folder `core backup` writes to without -o:
// the file's backup_dir, else flasher.DefaultBackupDir — the same folder
// a flash would have backed up to.
func configuredBackupDir() (string, error) {
	var s string
	if configDefault("backup_dir", &s) && strings.TrimSpace(s) != "" {
		return s, nil
	}
	return flasher.DefaultBackupDir()
}

// resolveGenreMap is --genre-map for sync and index when it was not
// typed: the file's genre_map, else the built-in map (or the repo's
// tools/artist_genres.json, for a developer editing it).
func resolveGenreMap(cmd *cobra.Command) string {
	var s string
	if configDefault("genre_map", &s) && strings.TrimSpace(s) != "" {
		noteFromConfig(cmd, "--genre-map", s)
		return s
	}
	return defaultGenreMap()
}

// resolvePlaylists is sync --playlists: the flag, else the file's
// playlists_dir, else "" (the syncer's <src>/Playlists).
func resolvePlaylists(cmd *cobra.Command, cur string) string {
	if cmd.Flags().Changed("playlists") {
		return cur
	}
	var s string
	if configDefault("playlists_dir", &s) && strings.TrimSpace(s) != "" {
		noteFromConfig(cmd, "--playlists", s)
		return s
	}
	return cur
}

// keepBackupsFlag is the hidden flag a parent hands keep_backups to its
// elevated child with, the way it hands over --backup-dir: the child may
// run as another account, whose config.json is not this user's. Nobody
// types it; the file is the setting.
const keepBackupsFlag = "keep-backups"

func addKeepBackupsFlag(cmd *cobra.Command, into *int) {
	cmd.Flags().IntVar(into, keepBackupsFlag, 0, "How many fwpart-* backups to keep (set by a parent for its elevated child)")
	_ = cmd.Flags().MarkHidden(keepBackupsFlag)
}

// resolveKeepBackups is keep_backups for flash, update and install: the
// hidden flag when a parent passed it, else the file, else 0 (keep all).
// A value from the file is said once, like every other.
func resolveKeepBackups(cmd *cobra.Command, cur int) int {
	if f := cmd.Flags().Lookup(keepBackupsFlag); f != nil && f.Changed {
		return cur
	}
	var n int
	if !configDefault("keep_backups", &n) || n <= 0 {
		return 0
	}
	noteFromConfig(cmd, "keep_backups", strconv.Itoa(n))
	return n
}
