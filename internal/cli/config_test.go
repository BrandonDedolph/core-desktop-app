package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain points every test in this package at a config file that does
// not exist. Commands now read config.json for their defaults, and a test
// that picked up the developer's own min_art_px or source folder would
// pass or fail by whose machine it ran on.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "core-cli-config-")
	if err != nil {
		panic(err)
	}
	missing := filepath.Join(dir, "core", "config.json")
	nameConfigPath = func() (string, error) { return missing, nil }
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// useConfig points the config path at a fresh temp file holding body
// ("" for no file at all) and returns the path.
func useConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "core", "config.json")
	if body != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := nameConfigPath
	nameConfigPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { nameConfigPath = old })
	return path
}

func readRaw(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, b)
	}
	return raw
}

func TestConfigPathPrintsTheFile(t *testing.T) {
	path := useConfig(t, "")
	out, _, err := runCore(t, "config", "path")
	if err != nil || strings.TrimSpace(out) != path {
		t.Fatalf("config path = %q, %v; want %s", out, err, path)
	}
}

func TestConfigSetGetUnsetRoundTrip(t *testing.T) {
	path := useConfig(t, "")

	// Absent is the default, and says so.
	out, _, err := runCore(t, "config", "get", "min_art_px")
	if err != nil || strings.TrimSpace(out) != "600 (default)" {
		t.Fatalf("get before set = %q, %v", out, err)
	}

	for _, tc := range []struct{ key, value, stored, shown string }{
		{"min_art_px", "1000", "1000", "1000"},
		{"min_art_px", "-1", "-1", "-1"},
		{"min_art_px", "off", "-1", "-1"},
		{"disc_folders", "on", "true", "true"},
		{"replaygain", "no", "false", "false"},
		{"theme", "Onyx", `"onyx"`, "onyx"},
		{"last_tab", "fix", `"fix"`, "fix"},
		{"music_view", "Songs", `"songs"`, "songs"},
		{"artist_pictures", "on", "true", "true"},
		{"volume", "0", "0", "0"},
		{"keep_backups", "3", "3", "3"},
		{"source", `C:\Music`, `"C:\\Music"`, `C:\Music`},
	} {
		if _, _, err := runCore(t, "config", "set", tc.key, tc.value); err != nil {
			t.Fatalf("set %s %s: %v", tc.key, tc.value, err)
		}
		if got := string(readRaw(t, path)[tc.key]); got != tc.stored {
			t.Errorf("set %s %s stored %s, want %s", tc.key, tc.value, got, tc.stored)
		}
		out, _, err := runCore(t, "config", "get", tc.key)
		if err != nil || strings.TrimSpace(out) != tc.shown {
			t.Errorf("get %s = %q, %v; want %q", tc.key, out, err, tc.shown)
		}
	}

	// unset deletes the key; it does not write the default.
	if _, _, err := runCore(t, "config", "unset", "min_art_px"); err != nil {
		t.Fatal(err)
	}
	if _, ok := readRaw(t, path)["min_art_px"]; ok {
		t.Error("unset left min_art_px in the file")
	}
	// The word that means the default deletes the key too: the app reads
	// theme "" as System and has no theme called "system".
	if _, _, err := runCore(t, "config", "set", "theme", "system"); err != nil {
		t.Fatal(err)
	}
	if _, ok := readRaw(t, path)["theme"]; ok {
		t.Error(`set theme system wrote a theme the app does not know`)
	}
	if _, _, err := runCore(t, "config", "set", "music_view", "albums"); err != nil {
		t.Fatal(err)
	}
	if _, ok := readRaw(t, path)["music_view"]; ok {
		t.Error(`set music_view albums wrote the default instead of removing the key`)
	}

	// The listing has every key, set or default.
	out, _, err = runCore(t, "config")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"disc_folders", "= true", "min_art_px", "= 600 (default)", "log_file", path} {
		if !strings.Contains(out, want) {
			t.Errorf("listing has no %q:\n%s", want, out)
		}
	}
	for _, k := range configKeys {
		if !strings.Contains(out, k.Name+" ") {
			t.Errorf("listing has no line for %s", k.Name)
		}
	}
}

// The file is the app's too. Every key this command does not own — the
// names, a window size, whatever the app grows next — must come out of a
// set exactly as it went in.
func TestConfigSetKeepsEveryOtherKey(t *testing.T) {
	before := map[string]json.RawMessage{
		"names":        json.RawMessage(`{"SERIAL123": "Brandon's iPod"}`),
		"window":       json.RawMessage(`{"w": 1200, "h": 800}`),
		"from_future":  json.RawMessage(`{"nested": [1, 2.50, "x"], "flag": true}`),
		"backup_dir":   json.RawMessage(`"D:\\backups"`),
		"replaygain":   json.RawMessage(`false`),
		"also_unknown": json.RawMessage(`"ünïcödé ’"`),
	}
	// Written the way the app writes it, so the bytes can be compared.
	body, err := json.MarshalIndent(before, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := useConfig(t, string(body)+"\n")

	if _, _, err := runCore(t, "config", "set", "min_art_px", "1000"); err != nil {
		t.Fatal(err)
	}
	after := readRaw(t, path)
	for k, v := range before {
		var want, got bytes.Buffer
		_ = json.Compact(&want, v)
		_ = json.Compact(&got, after[k])
		if want.String() != got.String() {
			t.Errorf("%s changed: was %s, now %s", k, v, after[k])
		}
	}
	if string(after["min_art_px"]) != "1000" {
		t.Errorf("min_art_px = %s", after["min_art_px"])
	}
	// And byte for byte: against the file as the app lays it out, the only
	// difference is the new key.
	got, _ := os.ReadFile(path)
	before["min_art_px"] = json.RawMessage("1000")
	want, _ := json.MarshalIndent(before, "", "  ")
	if string(got) != string(want)+"\n" {
		t.Errorf("file after set:\n%s\nwant:\n%s", got, want)
	}
}

func TestConfigRefusals(t *testing.T) {
	path := useConfig(t, `{"names": {"S": "x"}}`)
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"set", "no_such_key", "1"}, []string{`no setting called "no_such_key"`, "min_art_px", "source", "log_file"}},
		{[]string{"get", "nope"}, []string{"the settings are:"}},
		{[]string{"unset", "nope"}, []string{"the settings are:"}},
		{[]string{"set", "names", "x"}, []string{"core name"}},
		{[]string{"get", "names"}, []string{"core name"}},
		{[]string{"unset", "names"}, []string{"core name"}},
		{[]string{"set", "disc_folders", "maybe"}, []string{"true or false"}},
		{[]string{"set", "theme", "neon"}, []string{"system, linen, onyx"}},
		{[]string{"set", "last_tab", "albums"}, []string{"music, fix, summary"}},
		{[]string{"set", "update_check", "daily"}, []string{"connect, never"}},
		{[]string{"set", "min_art_px", "50"}, []string{"-1 or off, or a size of 100 px or more"}},
		{[]string{"set", "min_art_px", "0"}, []string{"100 px or more"}},
		{[]string{"set", "min_art_px", "big"}, []string{"whole number"}},
		{[]string{"set", "volume", "101"}, []string{"0 to 100"}},
		{[]string{"set", "keep_backups", "-2"}, []string{"0 (keep them all)"}},
		{[]string{"set", "window", "800x600"}, []string{"saved by the app's window"}},
	} {
		_, _, err := runCore(t, append([]string{"config"}, tc.args...)...)
		if err == nil {
			t.Errorf("config %v succeeded", tc.args)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("config %v: error %q does not say %q", tc.args, err, w)
			}
		}
	}
	// None of those touched the file.
	if raw := readRaw(t, path); len(raw) != 1 || string(raw["names"]) != `{"S": "x"}` {
		t.Errorf("a refused command changed the file: %v", raw)
	}
}

// A file that is not JSON is refused, not overwritten: one set over it
// would lose every setting and every iPod name.
func TestConfigRefusesToOverwriteACorruptFile(t *testing.T) {
	path := useConfig(t, `{"source": "C:\Music",`)
	if _, _, err := runCore(t, "config", "set", "volume", "50"); err == nil ||
		!strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("set over a corrupt file: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != `{"source": "C:\Music",` {
		t.Errorf("the corrupt file was rewritten: %s", b)
	}
}

func TestConfigSetWarnsAboutAMissingPath(t *testing.T) {
	useConfig(t, "")
	missing := filepath.Join(t.TempDir(), "not-here")
	_, stderr, err := runCore(t, "config", "set", "source", missing)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "does not exist") {
		t.Errorf("no warning for a missing folder: %q", stderr)
	}
	_, stderr, _ = runCore(t, "config", "set", "source", t.TempDir())
	if stderr != "" {
		t.Errorf("a folder that exists was warned about: %q", stderr)
	}
	// A log file only needs its folder.
	_, stderr, _ = runCore(t, "config", "set", "log_file", filepath.Join(t.TempDir(), "core.log"))
	if stderr != "" {
		t.Errorf("a new log file in an existing folder was warned about: %q", stderr)
	}
}

func TestConfigJSONPrintsTheObjectAsStored(t *testing.T) {
	useConfig(t, `{"names": {"S": "x"}, "min_art_px": 1000, "zzz": 1}`)
	out, _, err := runCore(t, "config", "--json")
	if err != nil {
		t.Fatal(err)
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("--json is not JSON: %v\n%s", err, out)
	}
	if len(raw) != 3 || string(raw["min_art_px"]) != "1000" {
		t.Errorf("--json = %s", out)
	}
}

// --- defaults from the file -------------------------------------------------

// --src: the flag beats $CORELIB_SRC, which beats the file's source.
func TestSrcPrecedenceFlagEnvFile(t *testing.T) {
	fileSrc := organizeFixture(t)
	envSrc := t.TempDir()
	flagSrc := t.TempDir()
	body, _ := json.Marshal(map[string]string{"source": fileSrc})
	useConfig(t, string(body))

	// The file alone: used, and said once on stderr.
	t.Setenv("CORELIB_SRC", "")
	out, stderr, err := runCore(t, "organize", "--json")
	if err != nil {
		t.Fatalf("organize from config: %v", err)
	}
	if want := "source: " + fileSrc + " (from core config)"; strings.Count(stderr, want) != 1 {
		t.Errorf("stderr does not say once where the source came from: %q", stderr)
	}
	if !strings.Contains(out, `"Moves"`) || !strings.Contains(out, filepath.Base(fileSrc)) {
		t.Errorf("organize did not read the configured source:\n%s", out)
	}

	// The environment beats the file.
	t.Setenv("CORELIB_SRC", envSrc)
	if got := resolveSrc(findCmd(t, "organize"), ""); got != envSrc {
		t.Errorf("with $CORELIB_SRC set, --src = %q, want %q", got, envSrc)
	}
	// The flag beats both.
	if got := resolveSrc(findCmd(t, "organize"), flagSrc); got != flagSrc {
		t.Errorf("with --src, --src = %q, want %q", got, flagSrc)
	}
	// And the commands that need a source name the config way out.
	useConfig(t, "")
	t.Setenv("CORELIB_SRC", "")
	for _, c := range []string{"fix", "organize"} {
		if _, _, err := runCore(t, c); err == nil || !strings.Contains(err.Error(), "core config set source") {
			t.Errorf("%s with no source: %v", c, err)
		}
	}
}

// fix --min-art: the flag beats min_art_px; -1 in the file is off.
func TestMinArtPrecedenceFlagFile(t *testing.T) {
	src := lowResDupFixture(t) // 400 px covers
	run := func(args ...string) (string, string) {
		t.Helper()
		out, stderr, err := runCore(t, append([]string{"fix", "--dry-run", "--src", src, "--journal-dir", t.TempDir()}, args...)...)
		if err != nil {
			t.Fatalf("fix %v: %v\n%s", args, err, out)
		}
		return out, stderr
	}

	useConfig(t, `{"min_art_px": 300}`)
	out, stderr := run()
	if !strings.Contains(out, "Low-res art (0) — under 300 px") {
		t.Errorf("min_art_px 300 not used:\n%s", out)
	}
	if !strings.Contains(stderr, "--min-art: 300 (from core config)") {
		t.Errorf("stderr does not say where 300 came from: %q", stderr)
	}
	out, _ = run("--min-art", "1000")
	if !strings.Contains(out, "Low-res art (2) — under 1000 px") {
		t.Errorf("--min-art 1000 did not beat the file:\n%s", out)
	}

	useConfig(t, `{"min_art_px": -1}`)
	if out, _ = run(); strings.Contains(out, "Low-res art") {
		t.Errorf("min_art_px -1 still checks:\n%s", out)
	}
	if out, _ = run("--min-art", "600"); !strings.Contains(out, "Low-res art (2) — under 600 px") {
		t.Errorf("--min-art 600 did not beat -1 in the file:\n%s", out)
	}

	// A value of the wrong type is ignored, as the app ignores it.
	useConfig(t, `{"min_art_px": "huge"}`)
	if out, _ = run(); !strings.Contains(out, "under 600 px") {
		t.Errorf("a mistyped min_art_px was not ignored:\n%s", out)
	}
}

// --disc-folders: the flag (either way) beats disc_folders.
func TestDiscFoldersPrecedenceFlagFile(t *testing.T) {
	src := t.TempDir()
	dir := filepath.Join(src, "Double - Band")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMultiDisc(t, dir)

	useConfig(t, `{"disc_folders": true}`)
	out, stderr, err := runCore(t, "organize", "--src", src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Disc 1/01 - Band - Alpha.flac") {
		t.Errorf("disc_folders true in the file did not split:\n%s", out)
	}
	if !strings.Contains(stderr, "--disc-folders: true (from core config)") {
		t.Errorf("stderr does not say where --disc-folders came from: %q", stderr)
	}
	out, _, _ = runCore(t, "organize", "--src", src, "--disc-folders=false")
	if strings.Contains(out, "Disc 1/") {
		t.Errorf("--disc-folders=false did not beat the file:\n%s", out)
	}
	out, _, _ = runCore(t, "fix", "--dry-run", "--names", "--organize", "--src", src, "--journal-dir", t.TempDir())
	if !strings.Contains(out, "Disc 1/") {
		t.Errorf("fix did not take disc_folders from the file:\n%s", out)
	}

	useConfig(t, `{"disc_folders": false}`)
	out, _, _ = runCore(t, "organize", "--src", src, "--disc-folders")
	if !strings.Contains(out, "Disc 1/") {
		t.Errorf("--disc-folders did not beat false in the file:\n%s", out)
	}
}

// --backup-dir: the flag beats backup_dir, and `core backup` writes into it.
func TestBackupDirFromConfig(t *testing.T) {
	dir := t.TempDir()
	body, _ := json.Marshal(map[string]string{"backup_dir": dir})
	useConfig(t, string(body))

	quiet := findCmd(t, "flash")
	quiet.SetErr(io.Discard)
	got, fromFile := resolveBackupDir(quiet, "")
	if got != dir || !fromFile {
		t.Errorf("flash --backup-dir from config = %q, %v; want %q", got, fromFile, dir)
	}
	p, err := defaultBackupPath(131475456, time.Now())
	if err != nil || filepath.Dir(p) != dir {
		t.Errorf("core backup default = %q, %v; want a file in %s", p, err, dir)
	}

	cmd := findCmd(t, "flash")
	cmd.SetErr(io.Discard)
	if err := cmd.Flags().Set("backup-dir", "E:\\elsewhere"); err != nil {
		t.Fatal(err)
	}
	if got, fromFile := resolveBackupDir(cmd, "E:\\elsewhere"); got != "E:\\elsewhere" || fromFile {
		t.Errorf("--backup-dir did not beat the file: %q, %v", got, fromFile)
	}
}
