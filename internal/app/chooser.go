package app

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// The chooser (docs/design/bench-2026-11-03.md, slice D1): every Browse…
// in the window opens this sheet instead of the operating system's dialog.
// The OS dialog was a PowerShell one-liner, and a windowsgui process that
// starts a console program gets a console window flashing up before the
// dialog — the first Windows bench of Settings found it on every Browse….
// So the app lists folders itself: the album sheet's chrome (a scrim, one
// plate, Esc or a click outside cancels), the current folder as crumbs, a
// scrolling list, and the choose button. Nothing is shelled out to.
//
// Every disk read is on its own goroutine — an iPod or a network drive can
// take seconds to list — and lands through drain (newChooserDir), keyed by a
// sequence number so a slow answer for a folder already left is dropped.

// chooserKind is what one Browse… asks for: the sheet's title, whether it
// wants a file or a folder, and for a file the filter.
type chooserKind struct {
	id    string // the key the last folder chosen is remembered under
	title string
	files bool
	// filter is the file kinds, in words, for the foot line; exts are the
	// extensions it lists (lower case, with the dot).
	filter string
	exts   []string
}

var (
	chooseMusicKind     = chooserKind{id: "music", title: "Choose the music folder"}
	choosePlaylistsKind = chooserKind{id: "playlists", title: "Choose the playlists folder"}
	chooseBackupKind    = chooserKind{id: "backup", title: "Choose the backup folder"}
	chooseAddKind       = chooserKind{id: "add", title: "Choose a folder of music to add"}
	chooseLogKind       = chooserKind{id: "log", title: "Choose the folder for the log"}
	chooseFirmwareKind  = chooserKind{id: "firmware", title: "Choose a firmware image", files: true,
		filter: "a firmware image (.ipod or .bin)", exts: []string{".ipod", ".bin"}}
	chooseGenreMapKind = chooserKind{id: "genremap", title: "Choose a genre map", files: true,
		filter: "a genre map (.json)", exts: []string{".json"}}
)

// chooserKinds is every kind, for the words test.
var chooserKinds = []chooserKind{chooseMusicKind, choosePlaylistsKind, chooseBackupKind, chooseLogKind,
	chooseFirmwareKind, chooseGenreMapKind, chooseAddKind}

// accepts reports whether a file called name is one this kind lists.
func (k chooserKind) accepts(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	for _, e := range k.exts {
		if ext == e {
			return true
		}
	}
	return false
}

// The sheet's words, in one place for the words test.
const (
	chThisPC     = "This PC"
	chComputer   = "Computer"
	chHome       = "Home"
	chLocalDisk  = "Local Disk"
	chUp         = "Up"
	chTypePath   = "Type a path"
	chTypeHint   = `A folder, then Enter — C:\Users\you\Music`
	chTypeHintNx = "A folder, then Enter — /home/you/Music"
	chReading    = "reading…"
	chCancel     = "Cancel"
	chChooseDir  = "Choose this folder"
	chChoose     = "Choose"
	chChooseFile = "Choose "
	chPickTop    = "Open a drive, or select a folder to choose it"
	chPickTopNx  = "Open a folder, or select one to choose it"
	chEmpty      = "Nothing to show in this folder"
	chEmptyDirs  = "No folders in this folder"
	chEmptyFiles = "No folders, and no files of the kind asked for"
	chUnreadable = "Could not read this folder: "
	chNotFolder  = "Not a folder: "
	chNoFolder   = "There is no folder at "
	chWhole      = "Type the whole path, from the drive"
	chWholeNx    = "Type the whole path, from /"
)

// chooserStrings is every string the chooser can show (bar the names it
// lists), for the words test.
func chooserStrings() []string {
	out := []string{chThisPC, chComputer, chHome, chLocalDisk, chUp, chTypePath, chTypeHint, chTypeHintNx,
		chReading, chCancel, chChooseDir, chChoose, chPickTop, chPickTopNx, chEmpty, chEmptyDirs, chEmptyFiles,
		chUnreadable, chNotFolder, chNoFolder, chWhole, chWholeNx, `Choose "Music"`}
	for _, k := range chooserKinds {
		out = append(out, k.title)
		if k.files {
			out = append(out, chChooseFile+k.filter)
		}
	}
	return out
}

// chooserEntry is one row: a folder, a file, or on the top level a drive
// (or Home and / off Windows).
type chooserEntry struct {
	name string
	path string
	dir  bool
	size int64
	root bool
}

// crumb is one step of the path line.
type crumb struct{ label, path string }

// chooserDir is one listing on its way from the reading goroutine to drain.
type chooserDir struct {
	seq     int
	at      string // the folder listed; "" is the top (This PC)
	entries []chooserEntry
	err     error
	// typeErr is set, and nothing else, when a typed path is not a folder:
	// the sheet keeps the listing it has and says so under the field.
	typeErr string
	// selPath is the entry to select once it lands: the folder Up came out
	// of, or the file a typed path named.
	selPath string
}

// chooserSheet is the open chooser. UI goroutine only.
type chooserSheet struct {
	kind    chooserKind
	goos    string
	home    string
	deliver func(string)

	at      string
	crumbs  []crumb
	entries []chooserEntry
	sel     int
	reading bool
	readErr string
	seq     int

	typing  bool
	typeErr string
	pathEd  widget.Editor

	list                 widget.List
	rows                 []widget.Clickable
	crumbClicks          []widget.Clickable
	upBtn, typeBtn       widget.Clickable
	cancelBtn, chooseBtn widget.Clickable
	scrim, block         widget.Clickable
}

// newChooser is a sheet for k with nothing listed yet.
func newChooser(k chooserKind, goos, home string) *chooserSheet {
	c := &chooserSheet{kind: k, goos: goos, home: home, sel: -1}
	c.list.Axis = layout.Vertical
	c.pathEd.SingleLine, c.pathEd.Submit = true, true
	c.crumbs = chooserCrumbs(goos, home, "")
	c.crumbClicks = make([]widget.Clickable, len(c.crumbs))
	return c
}

// selected is the selected row, or nil.
func (c *chooserSheet) selected() *chooserEntry {
	if c.sel < 0 || c.sel >= len(c.entries) {
		return nil
	}
	return &c.entries[c.sel]
}

// --- the pure parts --------------------------------------------------------

// topName is the top level's crumb.
func topName(goos string) string {
	if goos == "windows" {
		return chThisPC
	}
	return chComputer
}

// driveInfo is one drive letter that answered, with its volume label.
type driveInfo struct {
	letter string // "C"
	label  string // "" when the volume has none
}

// chooserRoots is the top level's rows: on Windows the drives, as Explorer
// names them ("Local Disk (C:)", "BRANDON'S I (D:)"); elsewhere the home
// folder and /.
func chooserRoots(goos, home string, drives []driveInfo) []chooserEntry {
	var out []chooserEntry
	if goos == "windows" {
		for _, d := range drives {
			label := d.label
			if label == "" {
				label = chLocalDisk
			}
			out = append(out, chooserEntry{name: label + " (" + d.letter + ":)", path: d.letter + `:\`,
				dir: true, root: true})
		}
		return out
	}
	if home != "" && home != "/" {
		out = append(out, chooserEntry{name: chHome, path: strings.TrimRight(home, "/"), dir: true, root: true})
	}
	return append(out, chooserEntry{name: "/", path: "/", dir: true, root: true})
}

// chooserCrumbs splits path into the path line's steps, the top level first:
// `This PC › C: › Users › you` for C:\Users\you, `Computer › Home › Music`
// for a folder under the home folder, `Computer › / › mnt` for one that is
// not. Each crumb carries the folder it opens. It is written per GOOS on
// strings, not with filepath, so all three platforms are tested from one.
func chooserCrumbs(goos, home, path string) []crumb {
	out := []crumb{{topName(goos), ""}}
	if path == "" {
		return out
	}
	sep := "/"
	var rootLabel, rootPath, rest string
	if goos == "windows" {
		sep = `\`
		p := strings.ReplaceAll(path, "/", `\`)
		switch {
		case len(p) >= 2 && p[1] == ':':
			rootLabel = strings.ToUpper(p[:2])
			rootPath, rest = rootLabel+`\`, p[2:]
		case strings.HasPrefix(p, `\\`):
			parts := strings.SplitN(strings.TrimPrefix(p, `\\`), `\`, 3)
			if len(parts) < 2 {
				return append(out, crumb{p, p})
			}
			rootLabel = `\\` + parts[0] + `\` + parts[1]
			rootPath = rootLabel
			if len(parts) == 3 {
				rest = parts[2]
			}
		default:
			return append(out, crumb{p, p})
		}
	} else {
		rootLabel, rootPath = "/", "/"
		if h := strings.TrimRight(home, "/"); h != "" && (path == h || strings.HasPrefix(path, h+"/")) {
			rootLabel, rootPath = chHome, h
		}
		rest = strings.TrimPrefix(path, rootPath)
	}
	out = append(out, crumb{rootLabel, rootPath})
	acc := rootPath
	for _, part := range strings.Split(rest, sep) {
		if part == "" {
			continue
		}
		if strings.HasSuffix(acc, sep) {
			acc += part
		} else {
			acc += sep + part
		}
		out = append(out, crumb{part, acc})
	}
	return out
}

// chooserParent is the folder above the last crumb, and false at the top.
func chooserParent(cs []crumb) (string, bool) {
	if len(cs) < 2 {
		return "", false
	}
	return cs[len(cs)-2].path, true
}

// hiddenName is the hidden rule that goes by name: a leading dot, and the
// two Windows system folders that are hidden by attribute most of the time
// but not on every volume (a FAT stick written by another machine).
func hiddenName(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	return strings.EqualFold(name, "$RECYCLE.BIN") || strings.EqualFold(name, "System Volume Information")
}

// sortChooser puts folders first, then files, each sorted without regard
// to case.
func sortChooser(es []chooserEntry) {
	sort.SliceStable(es, func(i, j int) bool {
		a, b := es[i], es[j]
		if a.dir != b.dir {
			return a.dir
		}
		la, lb := strings.ToLower(a.name), strings.ToLower(b.name)
		if la != lb {
			return la < lb
		}
		return a.name < b.name
	})
}

// chooseButton is the choose button's label, and whether it is enabled.
// Folder mode: "Choose this folder", or `Choose "Name"` with a folder row
// selected; at the top level only a selected row can be chosen. File mode:
// "Choose", with a file row selected.
func chooseButton(k chooserKind, at string, sel *chooserEntry) (string, bool) {
	if k.files {
		return chChoose, sel != nil && !sel.dir
	}
	if sel != nil && sel.dir {
		return `Choose "` + shortName(sel.name, 28) + `"`, true
	}
	return chChooseDir, at != ""
}

// chooserFoot is the muted line over the buttons: what Choose would return.
func chooserFoot(k chooserKind, goos, at string, sel *chooserEntry) string {
	if k.files {
		if sel != nil && !sel.dir {
			return sel.path
		}
		return chChooseFile + k.filter
	}
	if sel != nil && sel.dir {
		return sel.path
	}
	if at == "" {
		if goos == "windows" {
			return chPickTop
		}
		return chPickTopNx
	}
	return at
}

// chooserStart is the folder the chooser opens in: the field's path when it
// is a folder (or the folder a file in the field sits in), else the last
// folder chosen in this run for that kind, else the home folder, else the
// top level. isDir is os.Stat's answer, replaced in the tests.
func chooserStart(field, last, home string, isDir func(string) bool) string {
	field = strings.TrimSpace(field)
	var cands []string
	if field != "" && filepath.IsAbs(field) {
		cands = append(cands, field, filepath.Dir(field))
	}
	cands = append(cands, last, home)
	for _, c := range cands {
		if c != "" && isDir(c) {
			return filepath.Clean(c)
		}
	}
	return ""
}

// --- reading (off the UI goroutine) --------------------------------------

func isDirPath(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// readChooserDir lists dir for k: folders, and for a file kind the files
// it accepts, hidden entries skipped, sorted.
func readChooserDir(dir string, k chooserKind) ([]chooserEntry, error) {
	des, err := os.ReadDir(dir)
	if err != nil && len(des) == 0 {
		return nil, err
	}
	out := make([]chooserEntry, 0, len(des))
	for _, de := range des {
		name := de.Name()
		if hiddenName(name) {
			continue
		}
		info, ierr := de.Info()
		if ierr != nil || hiddenInfo(info) {
			continue
		}
		full := filepath.Join(dir, name)
		isDir, size := de.IsDir(), info.Size()
		if de.Type()&os.ModeSymlink != 0 {
			fi, serr := os.Stat(full)
			if serr != nil {
				continue
			}
			isDir, size = fi.IsDir(), fi.Size()
		}
		if !isDir && (!k.files || !k.accepts(name)) {
			continue
		}
		e := chooserEntry{name: name, path: full, dir: isDir}
		if !isDir {
			e.size = size
		}
		out = append(out, e)
	}
	sortChooser(out)
	return out, nil
}

// readChooser is one listing. typed marks a path somebody typed, which is
// checked first: not a folder, and the answer is only typeErr. A typed file
// of the kind asked for opens its folder with it selected.
func readChooser(k chooserKind, path, selPath string, typed bool) *chooserDir {
	if typed {
		path = strings.Trim(strings.TrimSpace(path), `"`)
		if !filepath.IsAbs(path) {
			if runtime.GOOS == "windows" {
				return &chooserDir{typeErr: chWhole}
			}
			return &chooserDir{typeErr: chWholeNx}
		}
		path = filepath.Clean(path)
		fi, err := os.Stat(path)
		switch {
		case err != nil:
			return &chooserDir{typeErr: chNoFolder + path}
		case !fi.IsDir() && k.files && k.accepts(fi.Name()):
			selPath, path = path, filepath.Dir(path)
		case !fi.IsDir():
			return &chooserDir{typeErr: chNotFolder + path}
		}
	}
	d := &chooserDir{at: path, selPath: selPath}
	if path == "" {
		home, _ := os.UserHomeDir()
		d.entries = chooserRoots(runtime.GOOS, home, listDrives())
		return d
	}
	d.entries, d.err = readChooserDir(path, k)
	return d
}

// --- the sheet's life ------------------------------------------------------

// openChooser opens the chooser for k. field is the path its field holds
// now, which decides where it opens; deliver gets the answer, on the UI
// goroutine, when Choose is pressed (and never on a cancel).
func (u *UI) openChooser(k chooserKind, field string, deliver func(string)) {
	home, _ := os.UserHomeDir()
	c := newChooser(k, runtime.GOOS, home)
	c.deliver = deliver
	u.chooser = c
	last := u.chooserLast[k.id]
	u.chooserRead(func() *chooserDir {
		return readChooser(k, chooserStart(field, last, home, isDirPath), "", false)
	})
}

// chooserGo lists path (the top level when ""), selecting selPath when it
// lands.
func (u *UI) chooserGo(path, selPath string) {
	k := u.chooser.kind
	u.chooserRead(func() *chooserDir { return readChooser(k, path, selPath, false) })
}

// chooserRead runs read on its own goroutine and hands the answer to drain.
func (u *UI) chooserRead(read func() *chooserDir) {
	c := u.chooser
	c.seq++
	c.reading = true
	seq := c.seq
	go func() {
		d := read()
		d.seq = seq
		u.mu.Lock()
		u.newChooserDir = d
		u.mu.Unlock()
		u.invalidate()
	}()
}

// applyChooserDir is drain's half: a listing for the folder the sheet is
// still waiting for replaces what it shows.
func (u *UI) applyChooserDir(d *chooserDir) {
	c := u.chooser
	if c == nil || d.seq != c.seq {
		return
	}
	c.reading = false
	if d.typeErr != "" {
		c.typeErr = d.typeErr
		return
	}
	c.typeErr, c.typing = "", false
	c.setListing(d)
}

// setListing shows d.
func (c *chooserSheet) setListing(d *chooserDir) {
	c.at, c.entries, c.readErr = d.at, d.entries, ""
	if d.err != nil {
		c.readErr = chUnreadable + d.err.Error()
	}
	c.crumbs = chooserCrumbs(c.goos, c.home, c.at)
	c.crumbClicks = make([]widget.Clickable, len(c.crumbs))
	c.rows = make([]widget.Clickable, len(c.entries))
	c.sel = -1
	for i, e := range c.entries {
		if d.selPath != "" && (e.path == d.selPath || (c.goos == "windows" && strings.EqualFold(e.path, d.selPath))) {
			c.sel = i
		}
	}
	c.list.Position = layout.Position{}
	c.scrollToSel()
}

// scrollToSel keeps the selected row on screen after a key moved it.
func (c *chooserSheet) scrollToSel() {
	p := &c.list.Position
	switch {
	case c.sel < 0:
	case c.sel < p.First || (c.sel == p.First && p.Offset > 0):
		p.First, p.Offset = c.sel, 0
	case p.Count > 1 && c.sel > p.First+p.Count-2:
		p.First, p.Offset = c.sel-p.Count+2, 0
	}
}

func (u *UI) closeChooser() { u.chooser = nil }

// chooserUp goes to the folder above, selecting the one it came out of.
func (u *UI) chooserUp() {
	c := u.chooser
	if parent, ok := chooserParent(c.crumbs); ok {
		u.chooserGo(parent, c.at)
	}
}

// chooserActivate is a second click on a row, or Enter: a folder opens, a
// file of the kind asked for is chosen.
func (u *UI) chooserActivate(i int) {
	c := u.chooser
	if i < 0 || i >= len(c.entries) {
		return
	}
	e := c.entries[i]
	if e.dir {
		u.chooserGo(e.path, "")
		return
	}
	if c.kind.files {
		c.sel = i
		u.chooserChoose()
	}
}

// chooserChoose is the choose button: the answer goes to deliver, and the
// folder it came from is remembered for the next Browse… of this kind.
func (u *UI) chooserChoose() {
	c := u.chooser
	sel := c.selected()
	if _, ok := chooseButton(c.kind, c.at, sel); !ok {
		return
	}
	path, folder := c.at, c.at
	if sel != nil {
		path = sel.path
		if sel.dir {
			folder = sel.path
		}
	}
	if folder != "" {
		if u.chooserLast == nil {
			u.chooserLast = map[string]string{}
		}
		u.chooserLast[c.kind.id] = folder
	}
	deliver := c.deliver
	u.chooser = nil
	if deliver != nil {
		deliver(path)
	}
}

// chooserEvents is the chooser's half of the frame.
func (u *UI) chooserEvents(gtx C) {
	c := u.chooser
	filters := []event.Filter{key.Filter{Name: key.NameEscape}}
	if !(c.typing && gtx.Focused(&c.pathEd)) {
		// While the path field has the keyboard, Enter and Backspace are
		// its own.
		filters = append(filters,
			key.Filter{Name: key.NameReturn}, key.Filter{Name: key.NameEnter},
			key.Filter{Name: key.NameDeleteBackward},
			key.Filter{Name: key.NameUpArrow}, key.Filter{Name: key.NameDownArrow})
	}
	for {
		ev, ok := gtx.Event(filters...)
		if !ok {
			break
		}
		ke, ok := ev.(key.Event)
		if !ok || ke.State != key.Press {
			continue
		}
		switch ke.Name {
		case key.NameEscape:
			u.closeChooser()
			return
		case key.NameReturn, key.NameEnter:
			u.chooserActivate(c.sel)
		case key.NameDeleteBackward:
			u.chooserUp()
		case key.NameUpArrow, key.NameDownArrow:
			c.moveSel(ke.Name == key.NameDownArrow)
		}
		if u.chooser != c {
			return
		}
	}
	if c.cancelBtn.Clicked(gtx) || c.scrim.Clicked(gtx) {
		u.closeChooser()
		return
	}
	c.block.Clicked(gtx) // a click on the plate is not a click outside it
	if c.chooseBtn.Clicked(gtx) {
		u.chooserChoose()
		return
	}
	if c.upBtn.Clicked(gtx) {
		u.chooserUp()
	}
	if c.typeBtn.Clicked(gtx) {
		c.typing, c.typeErr = !c.typing, ""
		if c.typing {
			c.pathEd.SetText(c.at)
			c.pathEd.SetCaret(c.pathEd.Len(), 0)
			gtx.Execute(key.FocusCmd{Tag: &c.pathEd})
		}
	}
	for {
		ev, ok := c.pathEd.Update(gtx)
		if !ok {
			break
		}
		switch ev.(type) {
		case widget.SubmitEvent:
			typed := c.pathEd.Text()
			k := c.kind
			u.chooserRead(func() *chooserDir { return readChooser(k, typed, "", true) })
		case widget.ChangeEvent:
			c.typeErr = ""
		}
	}
	for i := range c.crumbClicks {
		if c.crumbClicks[i].Clicked(gtx) && i < len(c.crumbs) && c.crumbs[i].path != c.at {
			u.chooserGo(c.crumbs[i].path, "")
		}
	}
	for i := range c.rows {
		if c.rows[i].Clicked(gtx) {
			if i == c.sel {
				u.chooserActivate(i)
				break
			}
			c.sel = i
		}
	}
}

// moveSel is an arrow key.
func (c *chooserSheet) moveSel(down bool) {
	n := len(c.entries)
	if n == 0 {
		return
	}
	switch {
	case c.sel < 0 && down:
		c.sel = 0
	case c.sel < 0:
		c.sel = n - 1
	case down && c.sel < n-1:
		c.sel++
	case !down && c.sel > 0:
		c.sel--
	}
	c.scrollToSel()
}

// --- drawing ----------------------------------------------------------------

// Chooser geometry.
const (
	chooserW    = 560
	chooserRowH = 26
)

// chooserLayer is the scrim and the plate: min(560 dp, 94 %) wide and 80 %
// of the window high, so the sheet does not jump as folders of different
// lengths are listed.
func (u *UI) chooserLayer(gtx C) D {
	c := u.chooser
	full := gtx.Constraints.Max
	paint.FillShape(gtx.Ops, u.pal.Scrim, clip.Rect{Max: full}.Op())
	c.scrim.Layout(gtx, func(gtx C) D { return D{Size: full} })

	w := full.X * 94 / 100
	if m := gtx.Dp(chooserW); w > m {
		w = m
	}
	sz := image.Pt(w, full.Y*80/100)
	off := image.Pt((full.X-sz.X)/2, (full.Y-sz.Y)/2)
	defer op.Offset(off).Push(gtx.Ops).Pop()
	r := gtx.Dp(8)
	box := image.Rectangle{Max: sz}
	paint.FillShape(gtx.Ops, u.pal.Border, clip.UniformRRect(box, r).Op(gtx.Ops))
	paint.FillShape(gtx.Ops, u.pal.Plate, clip.UniformRRect(box.Inset(gtx.Dp(1)), r-gtx.Dp(1)).Op(gtx.Ops))
	g := gtx
	g.Constraints = layout.Exact(sz)
	c.block.Layout(g, func(gtx C) D { return D{Size: sz} })
	layout.UniformInset(unit.Dp(18)).Layout(g, u.chooserBody)
	return D{Size: full}
}

// chooserBody is, top to bottom: the title, the path line, the list, the
// foot line and the buttons.
func (u *UI) chooserBody(gtx C) D {
	c := u.chooser
	sel := c.selected()
	label, ok := chooseButton(c.kind, c.at, sel)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			l := material.Label(u.th, unit.Sp(15), c.kind.title)
			l.Font.Weight = font.Bold
			l.Color = u.pal.Ink
			return l.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
		layout.Rigid(u.chooserPathLine),
		layout.Rigid(func(gtx C) D {
			if !c.typing || c.typeErr == "" {
				return D{}
			}
			return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, u.line(11.5, u.pal.Muted, c.typeErr))
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Flexed(1, u.chooserListBox),
		layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
		layout.Rigid(func(gtx C) D {
			l := material.Label(u.th, unit.Sp(11.5), chooserFoot(c.kind, c.goos, c.at, sel))
			l.Color = u.pal.Muted
			l.MaxLines = 1
			l.Truncator = "…"
			return l.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
		layout.Rigid(func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Max.X, 0)} }),
				layout.Rigid(func(gtx C) D {
					return u.buttonRow(gtx, btn{&c.cancelBtn, chCancel, false, true}, btn{&c.chooseBtn, label, true, ok})
				}),
			)
		}),
	)
}

// chooserPathLine is the crumbs (or the typed path's field), Up and
// Type a path.
func (u *UI) chooserPathLine(gtx C) D {
	c := u.chooser
	_, canUp := chooserParent(c.crumbs)
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx C) D {
			if c.typing {
				hint := chTypeHintNx
				if c.goos == "windows" {
					hint = chTypeHint
				}
				return u.field(gtx, &c.pathEd, hint)
			}
			return u.chooserCrumbsLine(gtx)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
		layout.Rigid(func(gtx C) D { return u.chooserLink(gtx, &c.upBtn, chUp, canUp) }),
		layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
		layout.Rigid(func(gtx C) D { return u.chooserLink(gtx, &c.typeBtn, chTypePath, true) }),
	)
}

// chooserLink is a small underlined link, or the same words muted and dead.
func (u *UI) chooserLink(gtx C, click *widget.Clickable, label string, enabled bool) D {
	if !enabled {
		l := material.Label(u.th, unit.Sp(12), label)
		l.Color = u.pal.SelSub
		l.MaxLines = 1
		return l.Layout(gtx)
	}
	return u.link(gtx, click, label, 12)
}

// chooserCrumbsLine draws as many crumbs as fit, from the end: the folder
// you are in is the one that matters, and the front of a deep path is the
// part to give up, behind a "…".
func (u *UI) chooserCrumbsLine(gtx C) D {
	c := u.chooser
	maxW := gtx.Constraints.Max.X
	sepW := u.crumbWidth(gtx, " › ", false)
	widths := make([]int, len(c.crumbs))
	for i, cr := range c.crumbs {
		widths[i] = u.crumbWidth(gtx, cr.label, i == len(c.crumbs)-1)
	}
	readW := 0
	if c.reading {
		readW = sepW + u.crumbWidth(gtx, chReading, false)
	}
	ellW := u.crumbWidth(gtx, "…", false) + sepW
	first, total := len(c.crumbs)-1, widths[len(c.crumbs)-1]+readW
	for first > 0 {
		need := total + widths[first-1] + sepW
		if first-1 > 0 {
			need += ellW
		}
		if need > maxW {
			break
		}
		first--
		total += widths[first] + sepW
	}
	var ch []layout.FlexChild
	plain := func(s string, col color.NRGBA) {
		ch = append(ch, layout.Rigid(func(gtx C) D {
			l := material.Label(u.th, unit.Sp(12.5), s)
			l.Color = col
			l.MaxLines = 1
			return l.Layout(gtx)
		}))
	}
	if first > 0 {
		plain("…", u.pal.Muted2)
		plain(" › ", u.pal.Muted2)
	}
	for i := first; i < len(c.crumbs); i++ {
		i := i
		if i > first {
			plain(" › ", u.pal.Muted2)
		}
		if i == len(c.crumbs)-1 {
			ch = append(ch, layout.Rigid(func(gtx C) D {
				l := material.Label(u.th, unit.Sp(12.5), c.crumbs[i].label)
				l.Color = u.pal.Ink
				l.Font.Weight = font.SemiBold
				l.MaxLines = 1
				l.Truncator = "…"
				return l.Layout(gtx)
			}))
			continue
		}
		ch = append(ch, layout.Rigid(func(gtx C) D {
			return c.crumbClicks[i].Layout(gtx, func(gtx C) D {
				l := material.Label(u.th, unit.Sp(12.5), c.crumbs[i].label)
				l.Color = u.pal.Muted
				l.MaxLines = 1
				d := l.Layout(gtx)
				defer clip.Rect{Max: d.Size}.Push(gtx.Ops).Pop()
				pointer.CursorPointer.Add(gtx.Ops)
				return d
			})
		}))
	}
	if c.reading {
		plain(" · "+chReading, u.pal.Muted2)
	}
	gtx.Constraints.Min.X = 0
	d := layout.Flex{Alignment: layout.Baseline}.Layout(gtx, ch...)
	// The whole width, so Up and Type a path sit at the line's right end.
	d.Size.X = maxW
	return d
}

// crumbWidth is one crumb's width, laid out into a throwaway macro.
func (u *UI) crumbWidth(gtx C, s string, bold bool) int {
	l := material.Label(u.th, unit.Sp(12.5), s)
	if bold {
		l.Font.Weight = font.SemiBold
	}
	l.MaxLines = 1
	gtx.Constraints.Min = image.Point{}
	m := op.Record(gtx.Ops)
	w := l.Layout(gtx).Size.X
	m.Stop()
	return w
}

// chooserListBox is the list on a Surface box with a hairline border, the
// dialogs' removal-list shape; an empty or unreadable folder says so.
func (u *UI) chooserListBox(gtx C) D {
	c := u.chooser
	sz := gtx.Constraints.Max
	r := gtx.Dp(6)
	box := image.Rectangle{Max: sz}
	paint.FillShape(gtx.Ops, u.pal.Border, clip.UniformRRect(box, r).Op(gtx.Ops))
	paint.FillShape(gtx.Ops, u.pal.Surface, clip.UniformRRect(box.Inset(gtx.Dp(1)), r-gtx.Dp(1)).Op(gtx.Ops))
	g := gtx
	g.Constraints = layout.Exact(sz)
	layout.UniformInset(unit.Dp(4)).Layout(g, func(gtx C) D {
		if len(c.entries) == 0 {
			msg := ""
			switch {
			case c.readErr != "":
				msg = c.readErr
			case c.reading:
				msg = ""
			case c.at == "":
				msg = chEmpty
			case c.kind.files:
				msg = chEmptyFiles
			default:
				msg = chEmptyDirs
			}
			if msg == "" {
				return D{Size: gtx.Constraints.Max}
			}
			return layout.UniformInset(unit.Dp(10)).Layout(gtx, u.wrapN(12, u.pal.Muted, msg, 3))
		}
		list := material.List(u.th, &c.list)
		list.Indicator.Color = u.pal.SelSub
		list.Indicator.HoverColor = u.pal.Muted2
		return list.Layout(gtx, len(c.entries), func(gtx C, i int) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, func(gtx C) D { return u.chooserRow(gtx, i) })
		})
	})
	return D{Size: sz}
}

// chooserRow is one row, 26 dp: the glyph, the name, and for a file its
// size in the right column. The selected row is the iPod's selected row —
// an Ink bar with Surface text.
func (u *UI) chooserRow(gtx C, i int) D {
	c := u.chooser
	e := c.entries[i]
	sz := image.Pt(gtx.Constraints.Max.X, gtx.Dp(chooserRowH))
	ink, muted := u.pal.Ink, u.pal.Muted
	return c.rows[i].Layout(gtx, func(gtx C) D {
		gtx.Constraints = layout.Exact(sz)
		if i == c.sel {
			paint.FillShape(gtx.Ops, u.pal.Ink, clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(4)).Op(gtx.Ops))
			ink, muted = u.pal.Surface, u.pal.Surface
		}
		func() {
			defer clip.Rect{Max: sz}.Push(gtx.Ops).Pop()
			pointer.CursorPointer.Add(gtx.Ops)
		}()
		layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					switch {
					case e.root:
						return driveGlyph(gtx, 14, ink)
					case e.dir:
						return folderGlyph(gtx, 14, ink)
					}
					return pageGlyph(gtx, 14, ink)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Flexed(1, func(gtx C) D {
					l := material.Label(u.th, unit.Sp(12.5), e.name)
					l.Color = ink
					l.Truncator = "…"
					if i == c.sel {
						// Thin dark letters on Onyx's light bar read grey.
						l.Font.Weight = font.SemiBold
					}
					return rowText(gtx, l, sz.Y)
				}),
				layout.Rigid(func(gtx C) D {
					if e.dir {
						return D{}
					}
					gtx.Constraints.Min.X = gtx.Dp(64)
					l := material.Label(u.th, unit.Sp(11.5), proseSize(e.size))
					l.Color = muted
					l.Alignment = text.End
					return rowText(gtx, l, sz.Y)
				}),
			)
		})
		return D{Size: sz}
	})
}

// --- glyphs ---------------------------------------------------------------
//
// Drawn the way the mark and the speaker are — clip paths in pixel space,
// no icon font — so they are crisp at any DPI and take the row's ink in
// either palette, and in the selected row's inverted colours.

// folderGlyph is a folder outline: a body with a tab on its top-left.
func folderGlyph(gtx C, size unit.Dp, col color.NRGBA) D {
	s := float32(gtx.Dp(size))
	w := s * 0.09
	if w < 1 {
		w = 1
	}
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(s*0.06, s*0.22))
	p.LineTo(f32.Pt(s*0.38, s*0.22))
	p.LineTo(f32.Pt(s*0.48, s*0.32))
	p.LineTo(f32.Pt(s*0.94, s*0.32))
	p.LineTo(f32.Pt(s*0.94, s*0.84))
	p.LineTo(f32.Pt(s*0.06, s*0.84))
	p.Close()
	paint.FillShape(gtx.Ops, col, clip.Stroke{Path: p.End(), Width: w}.Op())
	n := int(s + 0.5)
	return D{Size: image.Pt(n, n)}
}

// pageGlyph is a page with its top-right corner folded.
func pageGlyph(gtx C, size unit.Dp, col color.NRGBA) D {
	s := float32(gtx.Dp(size))
	w := s * 0.09
	if w < 1 {
		w = 1
	}
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(s*0.2, s*0.08))
	p.LineTo(f32.Pt(s*0.58, s*0.08))
	p.LineTo(f32.Pt(s*0.8, s*0.3))
	p.LineTo(f32.Pt(s*0.8, s*0.92))
	p.LineTo(f32.Pt(s*0.2, s*0.92))
	p.Close()
	paint.FillShape(gtx.Ops, col, clip.Stroke{Path: p.End(), Width: w}.Op())
	var f clip.Path
	f.Begin(gtx.Ops)
	f.MoveTo(f32.Pt(s*0.58, s*0.08))
	f.LineTo(f32.Pt(s*0.58, s*0.3))
	f.LineTo(f32.Pt(s*0.8, s*0.3))
	paint.FillShape(gtx.Ops, col, clip.Stroke{Path: f.End(), Width: w}.Op())
	n := int(s + 0.5)
	return D{Size: image.Pt(n, n)}
}

// driveGlyph is a drive: a flat box with its light on the right.
func driveGlyph(gtx C, size unit.Dp, col color.NRGBA) D {
	s := float32(gtx.Dp(size))
	w := s * 0.09
	if w < 1 {
		w = 1
	}
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32.Pt(s*0.06, s*0.32))
	p.LineTo(f32.Pt(s*0.94, s*0.32))
	p.LineTo(f32.Pt(s*0.94, s*0.74))
	p.LineTo(f32.Pt(s*0.06, s*0.74))
	p.Close()
	paint.FillShape(gtx.Ops, col, clip.Stroke{Path: p.End(), Width: w}.Op())
	paint.FillShape(gtx.Ops, col, clip.Outline{Path: circlePath(gtx, f32.Pt(s*0.76, s*0.53), s*0.07)}.Op())
	n := int(s + 0.5)
	return D{Size: image.Pt(n, n)}
}
