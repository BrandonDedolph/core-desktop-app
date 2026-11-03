package app

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"gioui.org/io/key"
)

// The chooser's pure parts are functions of GOOS and plain strings, so all
// three platforms are checked from here; the reading parts run on a temp
// tree; and one test goes the whole way, Browse… to st.Source.

func TestChooserRoots(t *testing.T) {
	got := chooserRoots("windows", `C:\Users\you`, []driveInfo{{"C", ""}, {"D", "BRANDON'S I"}})
	want := []chooserEntry{
		{name: "Local Disk (C:)", path: `C:\`, dir: true, root: true},
		{name: "BRANDON'S I (D:)", path: `D:\`, dir: true, root: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("windows roots = %+v", got)
	}
	for _, goos := range []string{"linux", "darwin"} {
		got = chooserRoots(goos, "/home/you/", nil)
		want = []chooserEntry{
			{name: "Home", path: "/home/you", dir: true, root: true},
			{name: "/", path: "/", dir: true, root: true},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s roots = %+v", goos, got)
		}
	}
	if got := chooserRoots("linux", "", nil); len(got) != 1 || got[0].path != "/" {
		t.Errorf("no home: %+v", got)
	}
}

func TestChooserCrumbs(t *testing.T) {
	for _, tc := range []struct {
		goos, home, path string
		labels, paths    []string
	}{
		{"windows", "", `C:\Users\you`,
			[]string{"This PC", "C:", "Users", "you"}, []string{"", `C:\`, `C:\Users`, `C:\Users\you`}},
		{"windows", "", `C:\`, []string{"This PC", "C:"}, []string{"", `C:\`}},
		{"windows", "", `d:/Music/`, []string{"This PC", "D:", "Music"}, []string{"", `D:\`, `D:\Music`}},
		{"windows", "", `\\nas\share\Albums`,
			[]string{"This PC", `\\nas\share`, "Albums"}, []string{"", `\\nas\share`, `\\nas\share\Albums`}},
		{"windows", "", "", []string{"This PC"}, []string{""}},
		{"linux", "/home/you", "/home/you", []string{"Computer", "Home"}, []string{"", "/home/you"}},
		{"linux", "/home/you", "/home/you/Music",
			[]string{"Computer", "Home", "Music"}, []string{"", "/home/you", "/home/you/Music"}},
		{"linux", "/home/other", "/home/you",
			[]string{"Computer", "/", "home", "you"}, []string{"", "/", "/home", "/home/you"}},
		// A sibling whose name starts with the home folder's is not in it.
		{"linux", "/home/you", "/home/young", []string{"Computer", "/", "home", "young"}, nil},
		{"darwin", "/Users/you", "/", []string{"Computer", "/"}, []string{"", "/"}},
	} {
		cs := chooserCrumbs(tc.goos, tc.home, tc.path)
		var labels, paths []string
		for _, c := range cs {
			labels, paths = append(labels, c.label), append(paths, c.path)
		}
		if !reflect.DeepEqual(labels, tc.labels) {
			t.Errorf("%s %q: labels %q, want %q", tc.goos, tc.path, labels, tc.labels)
		}
		if tc.paths != nil && !reflect.DeepEqual(paths, tc.paths) {
			t.Errorf("%s %q: paths %q, want %q", tc.goos, tc.path, paths, tc.paths)
		}
	}
	// Up from a drive is This PC; from This PC there is no up.
	if p, ok := chooserParent(chooserCrumbs("windows", "", `C:\`)); !ok || p != "" {
		t.Errorf("up from C: = %q %v", p, ok)
	}
	if _, ok := chooserParent(chooserCrumbs("windows", "", "")); ok {
		t.Error("This PC has an up")
	}
	if p, _ := chooserParent(chooserCrumbs("windows", "", `C:\Users\you`)); p != `C:\Users` {
		t.Errorf("up from C:\\Users\\you = %q", p)
	}
}

// writeTree makes the files and folders (a trailing / is a folder) under dir.
func writeTree(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		p := filepath.Join(dir, filepath.FromSlash(strings.TrimSuffix(n, "/")))
		if strings.HasSuffix(n, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(p, []byte("1234567"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func entryNames(es []chooserEntry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.name)
	}
	return out
}

// Folders first, then files, each sorted without regard to case; dot
// entries and the two Windows system folders never listed; a folder kind
// lists no files, a file kind only its own.
func TestChooserListingSortAndHidden(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, "b/", "A/", "c/", ".git/", "$RECYCLE.BIN/", "System Volume Information/",
		"z.ipod", "Y.BIN", "a.json", ".hidden.ipod", "notes.txt")
	for _, tc := range []struct {
		k    chooserKind
		want []string
	}{
		{chooseMusicKind, []string{"A", "b", "c"}},
		{chooseFirmwareKind, []string{"A", "b", "c", "Y.BIN", "z.ipod"}},
		{chooseGenreMapKind, []string{"A", "b", "c", "a.json"}},
	} {
		es, err := readChooserDir(dir, tc.k)
		if err != nil {
			t.Fatal(err)
		}
		if got := entryNames(es); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.k.id, got, tc.want)
		}
		for _, e := range es {
			if !e.dir && e.size != 7 {
				t.Errorf("%s: size %d", e.name, e.size)
			}
			if e.path != filepath.Join(dir, e.name) {
				t.Errorf("%s: path %q", e.name, e.path)
			}
		}
	}
	if _, err := readChooserDir(filepath.Join(dir, "gone"), chooseMusicKind); err == nil {
		t.Error("a folder that is not there listed")
	}
}

func TestChooserFilters(t *testing.T) {
	for name, want := range map[string]bool{"core.ipod": true, "CORE.IPOD": true, "fw.bin": true,
		"fw.bin.txt": false, "ipod": false, "map.json": false} {
		if got := chooseFirmwareKind.accepts(name); got != want {
			t.Errorf("firmware accepts %q = %v", name, got)
		}
	}
	if !chooseGenreMapKind.accepts("Genres.JSON") || chooseGenreMapKind.accepts("core.ipod") {
		t.Error("the genre map filter is wrong")
	}
	if chooseMusicKind.accepts("a.flac") {
		t.Error("a folder kind accepts a file")
	}
}

func TestChooseButtonLabel(t *testing.T) {
	folder := &chooserEntry{name: "Remain in Light", path: `C:\Music\Remain in Light`, dir: true}
	file := &chooserEntry{name: "core.ipod", path: `C:\core.ipod`}
	long := &chooserEntry{name: "A very long album folder name - Somebody", dir: true}
	for _, tc := range []struct {
		k     chooserKind
		at    string
		sel   *chooserEntry
		label string
		ok    bool
	}{
		{chooseMusicKind, `C:\Music`, nil, "Choose this folder", true},
		{chooseMusicKind, `C:\Music`, folder, `Choose "Remain in Light"`, true},
		{chooseMusicKind, "", nil, "Choose this folder", false},
		{chooseMusicKind, "", &chooserEntry{name: "Local Disk (C:)", path: `C:\`, dir: true, root: true},
			`Choose "Local Disk (C:)"`, true},
		{chooseMusicKind, `C:\`, long, `Choose "` + shortName(long.name, 28) + `"`, true},
		{chooseFirmwareKind, `C:\`, nil, "Choose", false},
		{chooseFirmwareKind, `C:\`, folder, "Choose", false},
		{chooseFirmwareKind, `C:\`, file, "Choose", true},
	} {
		label, ok := chooseButton(tc.k, tc.at, tc.sel)
		if label != tc.label || ok != tc.ok {
			t.Errorf("%s at %q sel %v: %q %v, want %q %v", tc.k.id, tc.at, tc.sel, label, ok, tc.label, tc.ok)
		}
	}
	// The foot line says what Choose would return.
	if got := chooserFoot(chooseMusicKind, "windows", `C:\Music`, folder); got != folder.path {
		t.Errorf("foot = %q", got)
	}
	if got := chooserFoot(chooseFirmwareKind, "windows", `C:\`, nil); !strings.Contains(got, ".ipod") {
		t.Errorf("file foot = %q", got)
	}
}

func TestChooserStart(t *testing.T) {
	// The fixture's paths have to be absolute on the OS the test runs on:
	// "/music" is not absolute on Windows, where it reads as a relative
	// path and the start falls through to the last folder (the first run
	// of this file's tests on Windows, 2026-11-03). abs is C:\music there.
	abs := func(p string) string {
		if p == "" || p == "relative" {
			return p
		}
		if runtime.GOOS == "windows" {
			return `C:` + filepath.FromSlash(p)
		}
		return p
	}
	dirs := map[string]bool{abs("/music"): true, abs("/logs"): true, abs("/last"): true, abs("/home/you"): true}
	isDir := func(p string) bool { return dirs[p] }
	for _, tc := range []struct{ field, last, home, want string }{
		{"/music", "/last", "/home/you", "/music"},
		{"/logs/core-app.log", "/last", "/home/you", "/logs"},
		{"", "/last", "/home/you", "/last"},
		{"/gone/x", "", "/home/you", "/home/you"},
		{"relative", "", "/home/you", "/home/you"},
		{"", "", "/nowhere", ""},
	} {
		if got := chooserStart(abs(tc.field), abs(tc.last), abs(tc.home), isDir); got != abs(tc.want) {
			t.Errorf("start(%q, %q, %q) = %q, want %q", tc.field, tc.last, tc.home, got, abs(tc.want))
		}
	}
}

// A typed path that is not a folder is said under the field and changes
// nothing; a typed file of the kind asked for opens its folder with it
// selected.
func TestChooserTypedPath(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, "core.ipod", "notes.txt")
	if d := readChooser(chooseMusicKind, filepath.Join(dir, "notes.txt"), "", true); !strings.HasPrefix(d.typeErr, chNotFolder) {
		t.Errorf("a file for a folder: %+v", d)
	}
	if d := readChooser(chooseMusicKind, filepath.Join(dir, "gone"), "", true); !strings.HasPrefix(d.typeErr, chNoFolder) {
		t.Errorf("nothing there: %+v", d)
	}
	if d := readChooser(chooseMusicKind, "relative/path", "", true); d.typeErr == "" {
		t.Errorf("a relative path was taken: %+v", d)
	}
	d := readChooser(chooseFirmwareKind, `"`+filepath.Join(dir, "core.ipod")+`"`, "", true)
	if d.typeErr != "" || d.at != dir || d.selPath != filepath.Join(dir, "core.ipod") {
		t.Errorf("a typed image: %+v", d)
	}
}

// waitChooser draws frames until the chooser's listing has landed.
func waitChooser(t *testing.T, u *UI) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for u.chooser != nil && u.chooser.reading {
		if time.Now().After(deadline) {
			t.Fatal("the chooser never listed its folder")
		}
		u.Layout(newTestContext(900, 600))
		time.Sleep(time.Millisecond)
	}
}

// Browse… to st.Source: the chooser opens where the field points, the
// listing lands through drain, Choose posts the folder the way the old
// picker did, and drain hands it to setSource.
func TestChooserChosenFolderReachesSource(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, "Albums/", "Other/", "cover.jpg")
	f := &fakeBackend{}
	u := newTestUI(f)
	u.sourceEd.SetText(root)
	u.pick(JobSync)
	if u.chooser == nil || u.chooser.kind.id != "music" {
		t.Fatalf("Browse… opened %+v", u.chooser)
	}
	waitChooser(t, u)
	c := u.chooser
	if c.at != root || !reflect.DeepEqual(entryNames(c.entries), []string{"Albums", "Other"}) {
		t.Fatalf("listing at %q: %q", c.at, entryNames(c.entries))
	}
	// A slow answer for a folder already left is dropped.
	u.mu.Lock()
	u.newChooserDir = &chooserDir{seq: c.seq - 1, at: "/stale"}
	u.mu.Unlock()
	u.Layout(newTestContext(900, 600))
	if c.at != root {
		t.Fatalf("a stale listing replaced the folder: %q", c.at)
	}

	// Down selects the first row; the button then names it.
	pressKey(u, key.NameDownArrow, 0)
	if c.sel != 0 {
		t.Fatalf("Down: sel %d", c.sel)
	}
	if label, _ := chooseButton(c.kind, c.at, c.selected()); label != `Choose "Albums"` {
		t.Errorf("label %q", label)
	}
	c.chooseBtn.Click()
	u.Layout(newTestContext(900, 600))
	if u.chooser != nil {
		t.Fatal("Choose left the chooser open")
	}
	u.drain()
	want := filepath.Join(root, "Albums")
	if u.st.Source != want {
		t.Fatalf("st.Source = %q, want %q", u.st.Source, want)
	}
	if u.chooserLast["music"] != want {
		t.Errorf("remembered %q", u.chooserLast["music"])
	}
}

// Enter opens the selected folder, Backspace goes back up with the folder
// it came out of selected, and Esc cancels without an answer.
func TestChooserKeys(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, "Albums/Inner/", "Other/")
	u := newTestUI(&fakeBackend{})
	answered := ""
	u.openChooser(chooseMusicKind, root, func(p string) { answered = p })
	waitChooser(t, u)
	c := u.chooser
	pressKey(u, key.NameDownArrow, 0)
	pressKey(u, key.NameReturn, 0)
	waitChooser(t, u)
	if c.at != filepath.Join(root, "Albums") || !reflect.DeepEqual(entryNames(c.entries), []string{"Inner"}) {
		t.Fatalf("Enter: at %q %q", c.at, entryNames(c.entries))
	}
	pressKey(u, key.NameDeleteBackward, 0)
	waitChooser(t, u)
	if c.at != root || c.selected() == nil || c.selected().name != "Albums" {
		t.Fatalf("Backspace: at %q sel %+v", c.at, c.selected())
	}
	pressKey(u, key.NameEscape, 0)
	if u.chooser != nil || answered != "" {
		t.Fatalf("Esc: chooser %v, answer %q", u.chooser != nil, answered)
	}
}

// A second click on the selected folder opens it; in a file kind a second
// click on a file chooses it.
func TestChooserSecondClick(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, "Images/", "Images/core.ipod")
	u := newTestUI(&fakeBackend{})
	answered := ""
	u.openChooser(chooseFirmwareKind, root, func(p string) { answered = p })
	waitChooser(t, u)
	c := u.chooser
	c.rows[0].Click()
	u.Layout(newTestContext(900, 600))
	if c.sel != 0 || c.reading {
		t.Fatalf("one click: sel %d reading %v", c.sel, c.reading)
	}
	c.rows[0].Click()
	u.Layout(newTestContext(900, 600))
	waitChooser(t, u)
	if c.at != filepath.Join(root, "Images") {
		t.Fatalf("second click: at %q", c.at)
	}
	c.rows[0].Click()
	u.Layout(newTestContext(900, 600))
	c.rows[0].Click()
	u.Layout(newTestContext(900, 600))
	if answered != filepath.Join(root, "Images", "core.ipod") || u.chooser != nil {
		t.Fatalf("a second click on a file: answered %q", answered)
	}
}

// Settings' Browse… opens the chooser over the sheet, and its answer lands
// in the field and the file (applyPicked) as the picker's did.
func TestChooserOverSettings(t *testing.T) {
	dir := t.TempDir()
	u := newTestUI(&fakeBackend{})
	u.openSettings()
	u.choosePath(pickPlaylists)
	if u.chooser == nil || u.chooser.kind.id != "playlists" || u.settings == nil {
		t.Fatal("Browse… did not open the chooser over Settings")
	}
	u.Layout(newTestContext(900, 600))
	u.chooser.deliver(dir)
	u.closeChooser()
	u.Layout(newTestContext(900, 600))
	if u.cfg.PlaylistsDir != dir || u.settings.playlistsEd.Text() != dir {
		t.Fatalf("playlists = %q, field %q", u.cfg.PlaylistsDir, u.settings.playlistsEd.Text())
	}
}

// The canned chooser is drawn from a fixed listing, Windows' crumbs and
// all, and reads no disk.
func TestCannedChooser(t *testing.T) {
	u := NewUI(Options{})
	u.SetState(DemoState())
	if err := cannedDialog(u, "chooser"); err != nil {
		t.Fatal(err)
	}
	c := u.chooser
	if c == nil || c.reading || len(c.entries) == 0 {
		t.Fatalf("canned chooser: %+v", c)
	}
	if got := c.crumbs[len(c.crumbs)-1].label; got != "Music" || c.crumbs[0].label != "This PC" {
		t.Errorf("crumbs %+v", c.crumbs)
	}
	if label, ok := chooseButton(c.kind, c.at, c.selected()); !ok || !strings.HasPrefix(label, `Choose "`) {
		t.Errorf("button %q %v", label, ok)
	}
	if d := u.Layout(newTestContext(900, 600)); d.Size.X != 900 {
		t.Errorf("size %v", d.Size)
	}
}

// Type a path: the field opens holding the current folder; a path that is
// not a folder stays in the field with the reason under it; a folder goes
// there and puts the crumbs back.
func TestChooserTypeAPath(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, "Albums/", "notes.txt")
	u := newTestUI(&fakeBackend{})
	u.openChooser(chooseMusicKind, root, func(string) {})
	waitChooser(t, u)
	c := u.chooser
	c.typeBtn.Click()
	u.Layout(newTestContext(900, 600))
	if !c.typing || c.pathEd.Text() != root {
		t.Fatalf("typing %v, field %q", c.typing, c.pathEd.Text())
	}
	submit := func(p string) {
		u.chooserRead(func() *chooserDir { return readChooser(c.kind, p, "", true) })
		waitChooser(t, u)
		u.Layout(newTestContext(900, 600))
	}
	submit(filepath.Join(root, "notes.txt"))
	if !c.typing || !strings.HasPrefix(c.typeErr, chNotFolder) || c.at != root {
		t.Fatalf("not a folder: typing %v err %q at %q", c.typing, c.typeErr, c.at)
	}
	submit(filepath.Join(root, "Albums"))
	if c.typing || c.typeErr != "" || c.at != filepath.Join(root, "Albums") {
		t.Fatalf("a folder: typing %v err %q at %q", c.typing, c.typeErr, c.at)
	}
}
