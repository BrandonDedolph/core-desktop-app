package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BrandonDedolph/core-desktop-app/internal/ghrelease"
)

// --- asset picking ------------------------------------------------------

// tenBinaries is the asset list release.yml attaches, with a checksum
// file beside each.
func tenBinaries() ghrelease.Release {
	var as []ghrelease.Asset
	for _, prog := range []string{App, CLI} {
		for _, p := range [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"},
			{"darwin", "arm64"}, {"windows", "amd64"}} {
			n := AssetName(prog, p[0], p[1])
			as = append(as, ghrelease.Asset{Name: n}, ghrelease.Asset{Name: n + ChecksumSuffix})
		}
	}
	return ghrelease.Release{Tag: "v0.4.0", Assets: as}
}

func TestPickAssetPerPlatform(t *testing.T) {
	rel := tenBinaries()
	cases := []struct{ prog, goos, goarch, want string }{
		{App, "windows", "amd64", "core-app-windows-amd64.exe"},
		{CLI, "windows", "amd64", "core-windows-amd64.exe"},
		{App, "linux", "arm64", "core-app-linux-arm64"},
		{CLI, "linux", "amd64", "core-linux-amd64"},
		{App, "darwin", "arm64", "core-app-darwin-arm64"},
		{CLI, "darwin", "amd64", "core-darwin-amd64"},
	}
	for _, c := range cases {
		p, err := PickAsset(rel, c.prog, c.goos, c.goarch)
		if err != nil {
			t.Errorf("%s %s/%s: %v", c.prog, c.goos, c.goarch, err)
			continue
		}
		if p.Bin.Name != c.want || p.Sum.Name != c.want+".sha256" {
			t.Errorf("%s %s/%s picked %q + %q, want %q", c.prog, c.goos, c.goarch, p.Bin.Name, p.Sum.Name, c.want)
		}
	}
	// "core" must not pick a core-app asset, which also starts "core-".
	p, _ := PickAsset(rel, CLI, "linux", "arm64")
	if strings.Contains(p.Bin.Name, "app") {
		t.Errorf("the CLI picked %s", p.Bin.Name)
	}
	// No windows/arm64 build is published.
	if _, err := PickAsset(rel, App, "windows", "arm64"); !errors.Is(err, ErrNoAsset) {
		t.Errorf("windows/arm64: %v, want ErrNoAsset", err)
	}
}

// A binary with no checksum beside it is refused, not trusted: the
// releases before this change carry exactly that.
func TestPickAssetRefusesWithoutAChecksum(t *testing.T) {
	rel := ghrelease.Release{Tag: "v0.3.0", Assets: []ghrelease.Asset{{Name: "core-app-windows-amd64.exe"}}}
	_, err := PickAsset(rel, App, "windows", "amd64")
	if !errors.Is(err, ErrNoChecksum) || !strings.Contains(err.Error(), "core-app-windows-amd64.exe.sha256") {
		t.Errorf("err = %v", err)
	}
}

func TestParseSum(t *testing.T) {
	h := strings.Repeat("ab", 32)
	o := strings.Repeat("cd", 32)
	cases := []struct {
		text string
		want string
		ok   bool
	}{
		{h + "  core-app-linux-amd64\n", h, true},
		{h + " *core-app-linux-amd64\n", h, true},
		{strings.ToUpper(h) + "\n", h, true}, // bare digest
		{o + "  core-linux-amd64\n" + h + "  core-app-linux-amd64\n", h, true},
		{o + "  core-linux-amd64\n", "", false},         // another file's sum
		{"deadbeef  core-app-linux-amd64\n", "", false}, // not a SHA-256
		{"", "", false},
	}
	for _, c := range cases {
		got, err := ParseSum(c.text, "core-app-linux-amd64")
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("ParseSum(%q) = %q, %v", c.text, got, err)
		}
	}
}

// --- download and verify --------------------------------------------------

type fakeRelease struct {
	*httptest.Server
	bodies map[string][]byte
	hits   int32
}

func newFakeRelease(t *testing.T) *fakeRelease {
	f := &fakeRelease{bodies: map[string][]byte{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := f.bodies[strings.TrimPrefix(r.URL.Path, "/dl/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&f.hits, 1)
		w.Write(b)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeRelease) add(name string, body []byte) ghrelease.Asset {
	f.bodies[name] = body
	return ghrelease.Asset{Name: name, Size: int64(len(body)), URL: f.URL + "/dl/" + name}
}

func sumOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// release publishes the linux/amd64 pair; sums overrides what each
// .sha256 file says.
func (f *fakeRelease) release(app, cli []byte, appSum, cliSum string) ghrelease.Release {
	a := AssetName(App, "linux", "amd64")
	c := AssetName(CLI, "linux", "amd64")
	return ghrelease.Release{Tag: "v0.4.0", Assets: []ghrelease.Asset{
		f.add(a, app), f.add(a+ChecksumSuffix, []byte(appSum+"  "+a+"\n")),
		f.add(c, cli), f.add(c+ChecksumSuffix, []byte(cliSum+"  "+c+"\n")),
	}}
}

// installed is a program folder: core-app and core, old bytes, 0755.
func installed(t *testing.T) (dir, app, cli string) {
	dir = t.TempDir()
	app, cli = filepath.Join(dir, "core-app"), filepath.Join(dir, "core")
	for _, p := range []string{app, cli} {
		if err := os.WriteFile(p, []byte("old "+filepath.Base(p)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir, app, cli
}

func updater(f *fakeRelease, cache string) *Updater {
	return &Updater{
		Client: &ghrelease.Client{HTTP: f.Client(), BaseURL: f.URL},
		GOOS:   "linux", GOARCH: "amd64",
		CachePath: func(tag, name string) (string, error) { return filepath.Join(cache, tag, name), nil },
	}
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			r, _ := filepath.Rel(dir, p)
			out = append(out, r)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func TestFetchVerifiesAndSwaps(t *testing.T) {
	f := newFakeRelease(t)
	newApp, newCLI := []byte("new app bytes"), []byte("new cli bytes")
	rel := f.release(newApp, newCLI, sumOf(newApp), sumOf(newCLI))
	dir, app, cli := installed(t)
	cache := t.TempDir()
	u := updater(f, cache)
	var progress []int64
	u.Progress = func(name string, done, total int64) {
		if name == "core-app-linux-amd64" {
			progress = append(progress, done, total)
		}
	}

	got, err := u.Fetch(context.Background(), rel, []Target{{App, app}, {CLI, cli}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(progress) < 2 || progress[len(progress)-2] != int64(len(newApp)) || progress[len(progress)-1] != int64(len(newApp)) {
		t.Errorf("progress = %v", progress)
	}
	// Fetch replaces nothing.
	if b, _ := os.ReadFile(app); string(b) != "old core-app" {
		t.Errorf("Fetch touched the app: %q", b)
	}
	if err := SwapAll("linux", got); err != nil {
		t.Fatalf("SwapAll: %v", err)
	}
	for p, want := range map[string][]byte{app: newApp, cli: newCLI} {
		b, _ := os.ReadFile(p)
		if string(b) != string(want) {
			t.Errorf("%s = %q", p, b)
		}
	}
	if l := listDir(t, dir); !reflect.DeepEqual(l, []string{"core", "core-app"}) {
		t.Errorf("program folder after the swap: %v", l)
	}
}

// A checksum that does not match: nothing is swapped, and neither the
// temp file nor a cache entry is left behind.
func TestFetchBadChecksumChangesNothing(t *testing.T) {
	f := newFakeRelease(t)
	newApp, newCLI := []byte("new app bytes"), []byte("new cli bytes")
	rel := f.release(newApp, newCLI, sumOf(newApp), sumOf([]byte("something else")))
	dir, app, cli := installed(t)
	cache := t.TempDir()

	_, err := updater(f, cache).Fetch(context.Background(), rel, []Target{{App, app}, {CLI, cli}})
	if !errors.Is(err, ghrelease.ErrChecksum) {
		t.Fatalf("err = %v, want ErrChecksum", err)
	}
	if b, _ := os.ReadFile(cli); string(b) != "old core" {
		t.Errorf("the CLI changed: %q", b)
	}
	if l := listDir(t, dir); !reflect.DeepEqual(l, []string{"core", "core-app"}) {
		t.Errorf("program folder: %v", l)
	}
	for _, p := range listDir(t, cache) {
		if strings.Contains(p, ".download-") || p == filepath.Join("v0.4.0", "core-linux-amd64") {
			t.Errorf("left in the cache: %s", p)
		}
	}
}

// A cached binary that has gone bad is not trusted: the cache entry
// fails its checksum and is fetched again.
func TestFetchRefetchesABadCache(t *testing.T) {
	f := newFakeRelease(t)
	newApp := []byte("new app bytes")
	rel := f.release(newApp, nil, sumOf(newApp), sumOf(nil))
	_, app, _ := installed(t)
	cache := t.TempDir()
	stale := filepath.Join(cache, "v0.4.0", "core-app-linux-amd64")
	os.MkdirAll(filepath.Dir(stale), 0o755)
	os.WriteFile(stale, []byte("bad app bytes"), 0o644) // same size, wrong bytes

	got, err := updater(f, cache).Fetch(context.Background(), rel, []Target{{App, app}})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if b, _ := os.ReadFile(got[0].New); string(b) != string(newApp) {
		t.Errorf("the stale cache was used: %q", b)
	}
}

func TestFetchRefusesAnUnwritableFolder(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder this user cannot write")
	}
	f := newFakeRelease(t)
	newApp := []byte("x")
	rel := f.release(newApp, newApp, sumOf(newApp), sumOf(newApp))
	dir, app, _ := installed(t)
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	_, err := updater(f, t.TempDir()).Fetch(context.Background(), rel, []Target{{App, app}})
	if !errors.Is(err, ErrNotWritable) || !strings.Contains(err.Error(), dir) {
		t.Fatalf("err = %v, want ErrNotWritable naming %s", err, dir)
	}
	if f.hits != 0 {
		t.Errorf("downloaded %d files for a folder it cannot write", f.hits)
	}
}

// --- the swap -------------------------------------------------------------

func newFile(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "download")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The Windows dance, on a temp dir: the running exe is renamed to .old,
// the new one takes its name, the sibling likewise, and CleanOld removes
// the .old files at the next start.
func TestSwapWindowsRenameDance(t *testing.T) {
	dir := t.TempDir()
	app, cli := filepath.Join(dir, "core-app.exe"), filepath.Join(dir, "core.exe")
	os.WriteFile(app, []byte("old app"), 0o755)
	os.WriteFile(cli, []byte("old cli"), 0o755)
	// A leftover from an earlier update whose cleanup never ran.
	os.WriteFile(filepath.Join(dir, "core.old.exe"), []byte("older cli"), 0o755)
	os.WriteFile(filepath.Join(dir, "notes.old.exe"), []byte("not ours"), 0o644)

	err := SwapAll("windows", []Fetched{
		{Target{App, app}, newFile(t, "new app")},
		{Target{CLI, cli}, newFile(t, "new cli")},
	})
	if err != nil {
		t.Fatalf("SwapAll: %v", err)
	}
	want := map[string]string{
		"core-app.exe": "new app", "core-app.old.exe": "old app",
		"core.exe": "new cli", "core.old.exe": "old cli", "notes.old.exe": "not ours",
	}
	if l := listDir(t, dir); len(l) != len(want) {
		t.Errorf("folder = %v", l)
	}
	for n, w := range want {
		if b, _ := os.ReadFile(filepath.Join(dir, n)); string(b) != w {
			t.Errorf("%s = %q, want %q", n, b, w)
		}
	}

	gone := CleanOld(dir)
	if len(gone) != 2 {
		t.Errorf("CleanOld removed %v", gone)
	}
	if l := listDir(t, dir); !reflect.DeepEqual(l, []string{"core-app.exe", "core.exe", "notes.old.exe"}) {
		t.Errorf("after CleanOld: %v", l)
	}
}

// A failure part-way through the Windows renames puts the old programs
// back under their own names.
func TestSwapWindowsRollsBack(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "core-app.exe")
	os.WriteFile(app, []byte("old app"), 0o755)
	cli := filepath.Join(dir, "core.exe")
	os.WriteFile(cli, []byte("old cli"), 0o755)
	// core.old.exe is a non-empty directory: it cannot be removed, so
	// the second rename cannot happen.
	os.MkdirAll(filepath.Join(dir, "core.old.exe", "x"), 0o755)

	err := SwapAll("windows", []Fetched{
		{Target{App, app}, newFile(t, "new app")},
		{Target{CLI, cli}, newFile(t, "new cli")},
	})
	if err == nil {
		t.Fatal("SwapAll succeeded")
	}
	for p, w := range map[string]string{app: "old app", cli: "old cli"} {
		if b, _ := os.ReadFile(p); string(b) != w {
			t.Errorf("%s = %q after the rollback", p, b)
		}
	}
	if _, err := os.Stat(OldName(app)); err == nil {
		t.Error("core-app.old.exe left after the rollback")
	}
	for _, n := range listDir(t, dir) {
		if strings.Contains(n, ".new-") {
			t.Errorf("staged file left: %s", n)
		}
	}
}

// On Linux and macOS the file is replaced in place and keeps its mode.
func TestSwapUnixKeepsTheModeBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "core-app")
	os.WriteFile(exe, []byte("old"), 0o750)
	os.Chmod(exe, 0o750)
	src := newFile(t, "new") // 0644 in the cache

	if err := SwapAll("linux", []Fetched{{Target{App, exe}, src}}); err != nil {
		t.Fatalf("SwapAll: %v", err)
	}
	st, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o750 {
		t.Errorf("mode = %v, want 0750", st.Mode().Perm())
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Errorf("exe = %q", b)
	}
	if l := listDir(t, dir); !reflect.DeepEqual(l, []string{"core-app"}) {
		t.Errorf("folder = %v (no .old on unix)", l)
	}
	// The cache copy is left alone.
	if _, err := os.Stat(src); err != nil {
		t.Errorf("the download went: %v", err)
	}
}

func TestOldName(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Core\core-app.exe`: `C:\Core\core-app.old.exe`,
		"/opt/core/core":       "/opt/core/core.old",
	} {
		if got := OldName(in); got != want {
			t.Errorf("OldName(%q) = %q", in, got)
		}
	}
}

// --- relaunch ---------------------------------------------------------------

func TestRelaunchStartsTheNewBinaryWithTheSameArgs(t *testing.T) {
	var started *exec.Cmd
	var code = -1
	ps, ex := startProcess, exit
	startProcess = func(c *exec.Cmd) error { started = c; return nil }
	exit = func(c int) { code = c }
	t.Cleanup(func() { startProcess, exit = ps, ex })

	if err := Relaunch("/opt/core/core-app", []string{"--source", "/music"}); err != nil {
		t.Fatal(err)
	}
	if started == nil || started.Path != "/opt/core/core-app" ||
		!reflect.DeepEqual(started.Args, []string{"/opt/core/core-app", "--source", "/music"}) {
		t.Errorf("started %+v", started)
	}
	if code != 0 {
		t.Errorf("exit code %d", code)
	}

	// Could not start: no exit, an error.
	code = -1
	startProcess = func(c *exec.Cmd) error { return errors.New("no such file") }
	if err := Relaunch("/x", nil); err == nil || code != -1 {
		t.Errorf("err %v, exit %d", err, code)
	}
}

func TestSibling(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "core-app.exe")
	os.WriteFile(exe, nil, 0o755)
	if _, ok := Sibling("windows", exe, CLI); ok {
		t.Error("found a core.exe that is not there")
	}
	os.WriteFile(filepath.Join(dir, "core.exe"), nil, 0o755)
	if p, ok := Sibling("windows", exe, CLI); !ok || p != filepath.Join(dir, "core.exe") {
		t.Errorf("Sibling = %q %v", p, ok)
	}
}

func TestTargets(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "core-app.exe")
	os.WriteFile(exe, nil, 0o755)
	if got := Targets("windows", exe, App); !reflect.DeepEqual(got, []Target{{App, exe}}) {
		t.Errorf("alone: %v", got)
	}
	cli := filepath.Join(dir, "core.exe")
	os.WriteFile(cli, nil, 0o755)
	if got := Targets("windows", exe, App); !reflect.DeepEqual(got, []Target{{App, exe}, {CLI, cli}}) {
		t.Errorf("with core.exe: %v", got)
	}
	if got := Targets("windows", cli, CLI); !reflect.DeepEqual(got, []Target{{CLI, cli}, {App, exe}}) {
		t.Errorf("from the CLI: %v", got)
	}
}
