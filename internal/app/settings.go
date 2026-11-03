package app

import (
	"context"
	"image"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/font"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/BrandonDedolph/core-desktop-app/internal/flasher"
)

// The Settings sheet (docs/design/settings-plan.md, slice C1): every
// standing choice the app remembers, in one place, saved the moment it
// changes. It is built like the album sheet — a scrim, one plate, one
// Close, a scrolling list, Esc or a click outside closes it — and its rows
// are the Summary tab's: the label in a 150 dp column, the control beside
// it.
//
// Nothing here is a second copy of anything. The music folder is the Fix
// tab's own field (a second door, not a second field), the four ticks are
// the Fix tab's own widget.Bools over the same State fields, the theme and
// ReplayGain call the handlers the ··· menu used to. So the two views
// cannot disagree, and every change goes through the one saveConfig.

// settingsSheet is the open sheet's own widgets. UI goroutine only.
type settingsSheet struct {
	list                   widget.List
	closeBtn, scrim, block widget.Clickable
	backupBrowse           widget.Clickable
	backupShow             widget.Clickable
	cfgShow, restoreBtn    widget.Clickable
	artClicks              [len(minArtChoices)]widget.Clickable
	themeClicks            [len(themeChoices)]widget.Clickable
	rgTick, winTick        widget.Bool
	tabTick                widget.Bool

	// C3: the behaviour rows.
	playlistsEd, genreEd, logEd       widget.Editor
	playlistsBrowse, playlistsDefault widget.Clickable
	genreBrowse, genreBuiltin         widget.Clickable
	logBrowse, logShow                widget.Clickable
	onlineTick, dupTick               widget.Bool
	artistTick                        widget.Bool
	verifyTick, ejectTick, preTick    widget.Bool
	keepClicks                        [len(keepChoices)]widget.Clickable
	updClicks                         [len(updateChoices)]widget.Clickable
}

// keepChoices are the Keep backups picker's stops, as keep_backups stores
// them: 0 is all, the default, so it is never written out.
var keepChoices = [3]struct {
	label string
	n     int
}{{"All", 0}, {"Last 3", 3}, {"Last 10", 10}}

// keepIndex is the stop a keep_backups value selects, or -1 for a
// hand-edited count the picker does not have.
func keepIndex(n int) int {
	for i, c := range keepChoices {
		if c.n == n || (c.n == 0 && n < 0) {
			return i
		}
	}
	return -1
}

// updateChoices are Check for updates' two stops, as update_check stores
// them ("" is the default, when an iPod connects).
var updateChoices = [2]struct {
	label, value string
}{{"When an iPod connects", ""}, {"Never", UpdateCheckNever}}

// logFileName is the name Diagnostics' Browse… gives the log in the folder
// chosen: the picker asks for a folder, and a name nobody has to invent.
const logFileName = "core-app.log"

// themeChoices are the Theme row's radio buttons, in order.
var themeChoices = [3]Theme{ThemeSystem, ThemeLinen, ThemeOnyx}

// minArtChoices are the low-resolution picker's four stops, as config.json
// stores them: below 0 is off, 0 is the default (600) — so the default is
// never written out — and the other two are the pixel count itself.
var minArtChoices = [4]struct {
	label string
	px    int
}{{"Off", -1}, {"300 px", 300}, {"600 px", 0}, {"1000 px", 1000}}

// minArtIndex is the picker stop a min_art_px value selects, or -1 for a
// hand-edited value that is none of the four (the row then says what it is).
func minArtIndex(px int) int {
	switch {
	case px < 0:
		return 0
	case px == 0 || px == 600:
		return 2
	case px == 300:
		return 1
	case px == 1000:
		return 3
	}
	return -1
}

// The sheet's words, in one place so the forbidden-word test reads the
// same strings the layout draws.
const (
	setTitle       = "Settings"
	setLibrary     = "Library"
	setFirmware    = "Firmware"
	setPlayback    = "Playback"
	setAppearance  = "Appearance"
	setMusicFolder = "Music folder"
	setFixAll      = "Fix all"
	setNames       = "Rename files to match their tags"
	setOrganize    = "Organize folders"
	setLowRes      = "Replace low-resolution covers"
	setDiscs       = "Split multi-disc albums into disc folders"
	setFixAllSub   = "what Fix all does — the same ticks as on the Fix tab"
	setSmallCovers = "Low-resolution covers"
	setSmallSub    = "a cover smaller than this on its shorter side is low-resolution: the Fix tab lists it and Fix all replaces it with a larger one. The iPod draws covers at 120 px, so this is about your music folder, not the iPod"
	setBackupDir   = "Backup folder"
	setBackupSub   = "the default — the command line puts its backups here too"
	setBackupSub2  = "chosen here; the command line puts its backups here too"
	setBrowse      = "Browse…"
	setShowFolder  = "Show folder"
	setRG          = "ReplayGain"
	setRGTick      = "Use ReplayGain"
	setRGSub       = "albums mastered loud play level with quiet ones, as on the iPod"
	setThemeRow    = "Theme"
	setWindow      = "Window"
	setWinTick     = "Remember the window size"
	setTabTick     = "Reopen the last tab"
	setFileIs      = "Settings are in "
	setRestore     = "Restore defaults"
	setFixed       = "The hold to write, the backup before every flash and the read-back check cannot be turned off."
	setClose       = "Close"

	// C3.
	setSync           = "Sync"
	setDiagnostics    = "Diagnostics"
	setPlaylists      = "Playlists folder"
	setPlaylistsEmpty = "the Playlists folder inside the music folder when empty"
	setDefault        = "Default"
	setCovers         = "Covers"
	setOnline         = "Look for covers on the internet"
	setOnlineSub      = "iTunes, then MusicBrainz and the Cover Art Archive; off, only a cover file in the album folder is used"
	setArtistTick     = "Artist pictures from Deezer"
	setArtistSub      = "the Music tab's artist photos from Deezer, whose photos they are, for private use; kept in this computer's cache, never put in the music folder or on the iPod. Off, an artist's picture is a mosaic of their covers"
	setArtistOffline  = "needs Look for covers on the internet, which is off: an artist's picture is a mosaic of their covers"
	setDuplicates     = "Duplicates"
	setDupTick        = "Check for duplicate albums"
	setCopies         = "Copies"
	setVerify         = "Verify copies by content"
	setVerifySub      = "slower; compares every byte instead of size and date"
	setEject          = "Eject when a sync finishes"
	setGenreMap       = "Genre map"
	setGenreSub       = "fills in a genre the tags leave out; the built-in map when empty"
	setBuiltin        = "Built-in"
	setKeep           = "Keep backups"
	setKeepSub        = "a flash backs the whole firmware partition up first; this is how many of those to keep — the Apple firmware backup is always kept"
	setUpdates        = "Check for updates"
	setPreRow         = "Pre-releases"
	setPre            = "Include pre-releases"
	setPreSub         = "for trying a release candidate on your own iPod"
	setLogFile        = "Log file"
	setLogSub         = "the window's log, appended to this file; takes effect at the next start"

	// The one sentence under each heading that says what the section is
	// for, and the sub-lines that answer "what happens if I change this"
	// for the rows whose label is a bare noun.
	setLibrarySub     = "Where your music is, and what Fix all is allowed to change in it."
	setSyncSub        = "How copies to the iPod are checked, and what happens after."
	setFirmwareSub    = "Backups taken before a flash, and how the app learns about releases."
	setPlaybackSub    = "Playing albums on this computer."
	setAppearanceSub  = "The window."
	setDiagnosticsSub = "For sending a log with a bug report."
	setDupSub         = "the Fix tab lists albums whose songs are also in another folder"
	setEjectSub       = "so the cable can be pulled as soon as the copy ends"
	setUpdatesSub     = "asks GitHub once, when an iPod connects; Check for updates in the ··· menu works either way"
	setThemeSub       = "System follows Windows' light or dark apps setting"
)

// settingsStrings is every string the sheet can show, for the words test.
func (u *UI) settingsStrings() []string {
	out := []string{setTitle, setLibrary, setFirmware, setPlayback, setAppearance, setMusicFolder,
		setFixAll, setNames, setOrganize, setLowRes, setDiscs, setFixAllSub, setSmallCovers, setSmallSub,
		setBackupDir, setBackupSub, setBackupSub2, setBrowse, setShowFolder, setRG, setRGTick, setRGSub,
		setThemeRow, setWindow, setWinTick, setTabTick, setFileIs + u.configPathShown(), setRestore, setFixed,
		setClose, u.backupDirShown(), restoreBody, u.minArtNote(),
		setSync, setDiagnostics, setPlaylists, setPlaylistsEmpty, u.playlistsSub(), setDefault, setCovers,
		setOnline, setOnlineSub, setArtistTick, setArtistSub, setArtistOffline, setDuplicates, setDupTick, setCopies, setVerify, setVerifySub, setEject,
		setGenreMap, setGenreSub, setBuiltin, setKeep, setKeepSub, u.keepNote(), setUpdates, setPreRow, setPre,
		setPreSub, setLogFile, setLogSub,
		setLibrarySub, setSyncSub, setFirmwareSub, setPlaybackSub, setAppearanceSub, setDiagnosticsSub,
		setDupSub, setEjectSub, setUpdatesSub, setThemeSub}
	for _, c := range minArtChoices {
		out = append(out, c.label)
	}
	for _, c := range keepChoices {
		out = append(out, c.label)
	}
	for _, c := range updateChoices {
		out = append(out, c.label)
	}
	for _, t := range themeChoices {
		out = append(out, u.themeLabel(t))
	}
	return out
}

// openSettings opens the sheet with its own ticks read from the config.
func (u *UI) openSettings() {
	s := &settingsSheet{}
	s.list.Axis = layout.Vertical
	s.rgTick.Value = u.replayGainOn()
	s.winTick.Value = on(u.cfg.WindowRemember)
	s.tabTick.Value = on(u.cfg.LastTabRemember)
	for _, ed := range []*widget.Editor{&s.playlistsEd, &s.genreEd, &s.logEd} {
		ed.SingleLine, ed.Submit = true, true
	}
	s.playlistsEd.SetText(u.cfg.PlaylistsDir)
	s.genreEd.SetText(u.cfg.GenreMap)
	s.logEd.SetText(u.cfg.LogFile)
	s.onlineTick.Value = u.cfg.OnlineArtOn()
	s.artistTick.Value = u.cfg.ArtistPictures
	s.dupTick.Value = u.cfg.CheckDuplicatesOn()
	s.verifyTick.Value = u.cfg.VerifySync
	s.ejectTick.Value = u.cfg.EjectAfterSync
	s.preTick.Value = u.cfg.Prereleases
	u.systemNow = ThemeSystem.Resolve()
	u.settings = s
}

// closeSettings closes it. Everything is already saved.
func (u *UI) closeSettings() { u.settings = nil }

// --- the live settings ---------------------------------------------------

// liveSettings is what a Backend offers when its settings can change under
// it without a restart: RealBackend does; a test's fake need not.
type liveSettings interface {
	SetBackupDir(dir string)
	SetMinArtPx(px int)
	SetOnlineArt(on bool)
	SetArtistPictures(on bool)
	SetCheckDuplicates(on bool)
	SetPrereleases(on bool)
	SetKeepBackups(n int)
}

// live is the backend's liveSettings, or nil for one that has none.
func (u *UI) live() liveSettings {
	ls, _ := u.be.(liveSettings)
	return ls
}

// --- the behaviour rows (C3) ----------------------------------------------

// setOnlineArt is Look for covers on the internet. The backend's art
// client goes (or comes back) for every later lookup; the Fix tab's cover
// rows say which it is from u.cfg.
func (u *UI) setOnlineArt(v bool) {
	u.cfg.OnlineArt = onPtr(v)
	u.saveConfig()
	if ls := u.live(); ls != nil {
		ls.SetOnlineArt(v)
	}
}

// setArtistPictures is Artist pictures from Deezer. The artist tiles ask
// for their picture again (the cache key carries the choice), so the photos
// arrive — or the mosaics come back — without a rescan.
func (u *UI) setArtistPictures(v bool) {
	u.cfg.ArtistPictures = v
	u.saveConfig()
	if ls := u.live(); ls != nil {
		ls.SetArtistPictures(v)
	}
}

// artistSub is the Deezer tick's sub-line: what it does, or why it cannot.
func (u *UI) artistSub() string {
	if !u.cfg.OnlineArtOn() {
		return setArtistOffline
	}
	return setArtistSub
}

// setCheckDuplicates is Check for duplicate albums: every later scan, and
// one now, so the Fix tab's Duplicate albums row appears or goes.
func (u *UI) setCheckDuplicates(v bool) {
	u.cfg.CheckDuplicates = onPtr(v)
	u.saveConfig()
	if ls := u.live(); ls != nil {
		ls.SetCheckDuplicates(v)
	}
	u.rescanSoon()
}

// setPrereleases is Include pre-releases, for the next check.
func (u *UI) setPrereleases(v bool) {
	u.cfg.Prereleases = v
	u.saveConfig()
	if ls := u.live(); ls != nil {
		ls.SetPrereleases(v)
	}
}

// setKeepBackups is the Keep backups picker, for the next flash.
func (u *UI) setKeepBackups(n int) {
	if n == u.cfg.KeepBackups {
		return
	}
	u.cfg.KeepBackups = n
	u.saveConfig()
	if ls := u.live(); ls != nil {
		ls.SetKeepBackups(n)
	}
}

// keepNote is the Keep backups sub-line, or what a hand-edited count is.
func (u *UI) keepNote() string {
	if n := u.cfg.KeepBackups; keepIndex(n) < 0 {
		return "now the last " + itoa(n) + ", set in the settings file; " + setKeepSub
	}
	return setKeepSub
}

// playlistsSub is the Playlists folder's sub-line: the folder an empty
// field means, named, when the music folder is known.
func (u *UI) playlistsSub() string {
	src := strings.TrimSpace(u.st.Source)
	if src == "" {
		return setPlaylistsEmpty
	}
	sep := "/"
	if strings.Contains(src, `\`) {
		sep = `\`
	}
	return strings.TrimRight(src, `/\`) + sep + "Playlists when empty"
}

// setPath is the three path fields' save: config.json at once, as the
// music folder is saved as it is typed. Nothing reads them until the next
// sync (or, for the log, the next start).
func (u *UI) setPath(dst *string, v string) {
	v = strings.TrimSpace(v)
	if v == *dst {
		return
	}
	*dst = v
	u.saveConfig()
}

// pathPick says which Settings field a picker's answer is for.
type pathPick int

const (
	pickPlaylists pathPick = iota + 1
	pickGenreMap
	pickLogFolder
)

// pickedPath is one picker answer on its way from the dialog's goroutine
// to drain.
type pickedPath struct {
	kind pathPick
	path string
}

// choosePath is Browse… on one of the three C3 path rows: the chooser, its
// answer landing in drain (applyPicked).
func (u *UI) choosePath(kind pathPick) {
	var k chooserKind
	var field string
	switch kind {
	case pickGenreMap:
		k, field = chooseGenreMapKind, u.cfg.GenreMap
	case pickPlaylists:
		k, field = choosePlaylistsKind, u.cfg.PlaylistsDir
	default:
		k, field = chooseLogKind, u.cfg.LogFile
	}
	u.openChooser(k, field, func(path string) {
		u.mu.Lock()
		u.newPicked = &pickedPath{kind, path}
		u.mu.Unlock()
		u.invalidate()
	})
}

// applyPicked puts a picker's answer in its field and the file.
func (u *UI) applyPicked(p pickedPath) {
	var ed *widget.Editor
	var dst *string
	path := p.path
	switch p.kind {
	case pickPlaylists:
		dst = &u.cfg.PlaylistsDir
	case pickGenreMap:
		dst = &u.cfg.GenreMap
	case pickLogFolder:
		dst = &u.cfg.LogFile
		path = filepath.Join(path, logFileName)
	default:
		return
	}
	if s := u.settings; s != nil {
		switch p.kind {
		case pickPlaylists:
			ed = &s.playlistsEd
		case pickGenreMap:
			ed = &s.genreEd
		default:
			ed = &s.logEd
		}
		ed.SetText(path)
	}
	u.setPath(dst, path)
}

// tickEvents is the four ticks' half of the frame, from the Fix tab and
// from Settings alike (only one of the two is reading events on a frame).
func (u *UI) tickEvents(gtx C, busy bool) {
	changed := u.lowResTick.Update(gtx)
	changed = u.namesTick.Update(gtx) || changed
	changed = u.organizeTick.Update(gtx) || changed
	disc := u.discTick.Update(gtx)
	if changed || disc {
		u.ticksChanged(disc, busy)
	}
}

// ticksChanged takes the four tick boxes into the model and config.json,
// saved at once. A changed multi-disc tick also rescans: the split has to
// be IN the plan before it can be applied, and only an Inspect with the
// option on puts it there (librarian's Options.DiscFolders). The row says
// so, and the scan is a read.
func (u *UI) ticksChanged(disc, busy bool) {
	u.st.FixNames, u.st.Organize = u.namesTick.Value, u.organizeTick.Value
	u.st.FixLowRes, u.st.DiscFolders = u.lowResTick.Value, u.discTick.Value
	u.cfg.FixNames, u.cfg.FixOrganize = onPtr(u.st.FixNames), onPtr(u.st.Organize)
	u.cfg.FixLowRes, u.cfg.DiscFolders = onPtr(u.st.FixLowRes), u.st.DiscFolders
	u.saveConfig()
	if disc && !busy {
		u.rescanSoon()
	}
}

// rescanSoon queues one scan of the music folder for the next frame with
// nothing running — once, however many settings changed meanwhile.
func (u *UI) rescanSoon() {
	if u.rescanQueued || strings.TrimSpace(u.st.Source) == "" {
		return
	}
	u.rescanQueued = true
	u.later(func() {
		u.rescanQueued = false
		u.startInspect(u.st.Source)
	})
}

// setMinArtPx is the low-resolution picker: config.json, the backend for
// every later scan, and a rescan now so the Fix tab's Low-resolution row
// lists what the new line says.
func (u *UI) setMinArtPx(px int) {
	if px == u.cfg.MinArtPx {
		return
	}
	u.cfg.MinArtPx = px
	u.saveConfig()
	if ls, ok := u.be.(liveSettings); ok {
		ls.SetMinArtPx(px)
	}
	u.rescanSoon()
}

// minArtNote is the picker's sub-line: what the line means, or, for a
// hand-edited value the four stops do not have, what it is now.
func (u *UI) minArtNote() string {
	if px := u.cfg.MinArtPx; minArtIndex(px) < 0 {
		return "now " + itoa(px) + " px, set in the settings file"
	}
	return setSmallSub
}

// setBackupDir is the Backup folder row: the backend's folder for every
// later write, config.json, and the Summary tab's Backups row read again
// from the new folder.
func (u *UI) setBackupDir(dir string) {
	dir = strings.TrimSpace(dir)
	if dir == u.cfg.BackupDir {
		return
	}
	u.cfg.BackupDir = dir
	u.saveConfig()
	if ls, ok := u.be.(liveSettings); ok {
		ls.SetBackupDir(dir)
	}
	u.st.Logf("backups now go to %s", u.backupDirShown())
	if u.be != nil {
		serial := u.st.Device.Serial
		go func() {
			u.readBackups(context.Background(), serial)
			u.invalidate()
		}()
	}
}

// backupDirShown is the folder the Backup folder row names: the chosen
// one, else the default the CLI uses too. A canned state (no backend)
// shows its own folder, so a screenshot never prints this machine's.
func (u *UI) backupDirShown() string {
	if u.cfg.BackupDir != "" {
		return u.cfg.BackupDir
	}
	if u.be == nil {
		return u.st.Backups.Dir
	}
	d, _ := flasher.DefaultBackupDir()
	return d
}

// cannedConfigPath is the settings file a screenshot names: the Windows
// path, with nobody's user name in it.
const cannedConfigPath = `C:\Users\you\AppData\Roaming\core\config.json`

// configPathShown is the footer's settings file.
func (u *UI) configPathShown() string {
	if u.cfgPath != "" {
		return u.cfgPath
	}
	if u.be == nil {
		return cannedConfigPath
	}
	p, _ := ConfigPath()
	return p
}

// chooseBackupFolder is the Backup folder's Browse…: the chooser, its answer
// landing in drain like every Browse…'s.
func (u *UI) chooseBackupFolder() {
	u.openChooser(chooseBackupKind, u.backupDirShown(), func(path string) {
		u.mu.Lock()
		u.newBackupDir = path
		u.mu.Unlock()
		u.invalidate()
	})
}

// openDir shows a folder in the file manager, creating it first: Explorer
// pointed at a folder that does not exist opens Documents, which is a lie
// about where things go.
func (u *UI) openDir(dir string) {
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		u.st.Logf("could not create %s: %v", dir, err)
		return
	}
	if err := openFolder(dir); err != nil {
		u.st.Logf("could not open %s: %v", dir, err)
	}
}

// setWindowRemember is "Remember the window size". Off forgets the size
// that was saved (the next start is 980×700) and stops saving; on saves
// the size the window has now, if it is not the default.
func (u *UI) setWindowRemember(v bool) {
	u.cfg.WindowRemember = onPtr(v)
	u.cfg.Window = nil
	u.win.dirty = false
	if v {
		// Saved by the next frame's saveWindowLater (nothing, when it is
		// the default).
		u.win.dirty, u.win.changedAt = true, time.Time{}
	}
	u.saveConfig()
}

// setTabRemember is "Reopen the last tab". Off forgets the tab; on saves
// the one showing now.
func (u *UI) setTabRemember(v bool) {
	u.cfg.LastTabRemember = onPtr(v)
	u.cfg.LastTab = ""
	if v {
		u.cfg.LastTab = lastTabValue(u.st.Tab)
	}
	u.savedTab = u.st.Tab
	u.saveConfig()
}

// lastTabValue is last_tab for a tab: "" for Music, the default.
func lastTabValue(t Tab) string {
	if t == TabAlbums {
		return ""
	}
	return tabKey(t)
}

// rememberTab saves the tab when it has changed since the last frame and
// "Reopen the last tab" is on. It runs once a frame, after the events, so
// every way the tab changes — a tab click, the footer's folder, a menu
// item that shows its result on the Summary tab — is caught in one place.
func (u *UI) rememberTab() {
	if u.st.Tab == u.savedTab {
		return
	}
	u.savedTab = u.st.Tab
	if !on(u.cfg.LastTabRemember) {
		return
	}
	if v := lastTabValue(u.st.Tab); v != u.cfg.LastTab {
		u.cfg.LastTab = v
		u.saveConfig()
	}
}

// winState is the window size's memory: the last size seen, and a change
// config.json has not got yet (saved after the same quiet the volume
// waits for, so a drag of the window's corner is one write).
type winState struct {
	size      image.Point // dp
	changedAt time.Time
	dirty     bool
}

// WindowResized is the window's ConfigEvent: its size in pixels, the
// metric of the last frame (ConfigEvent carries none) and whether the
// window is in its ordinary mode — a maximized or full-screen size is
// not a size anyone chose, and restoring it as a plain window would open
// a window the size of the screen with its title bar off the top.
func (u *UI) WindowResized(px image.Point, m unit.Metric, windowed bool) {
	if !windowed || m.PxPerDp <= 0 {
		return
	}
	dp := image.Pt(int(math.Round(float64(px.X)/float64(m.PxPerDp))),
		int(math.Round(float64(px.Y)/float64(m.PxPerDp))))
	// win.size starts as the size the window was opened at (NewUI), so
	// the first report — that same size — writes nothing.
	if dp.X < MinWidth || dp.Y < MinHeight || dp == u.win.size {
		return
	}
	u.win.size = dp
	if on(u.cfg.WindowRemember) {
		u.win.dirty, u.win.changedAt = true, u.now()
	}
}

// saveWindowLater writes the window size once it has been still for
// volSaveAfter. A size back at the default removes the key.
func (u *UI) saveWindowLater(gtx C) {
	if !u.win.dirty {
		return
	}
	due := u.win.changedAt.Add(volSaveAfter)
	if u.now().Before(due) {
		gtx.Execute(op.InvalidateCmd{At: due})
		return
	}
	u.win.dirty = false
	if !on(u.cfg.WindowRemember) {
		return
	}
	if u.win.size == image.Pt(DefaultWindowW, DefaultWindowH) {
		u.cfg.Window = nil
	} else {
		u.cfg.Window = &WindowSize{W: u.win.size.X, H: u.win.size.Y}
	}
	u.saveConfig()
}

// restoreBody is the Restore defaults question.
const restoreBody = "Every setting goes back to how it was the first time the app opened — the theme, " +
	"the Fix all ticks, the low-resolution line, the backup folder, ReplayGain, the volume, the window " +
	"size and the tab, and the cover, sync, update and log choices. The music folder and your iPods' " +
	"names are kept."

// askRestore asks once, in the ordinary dialog, and restores on a yes.
func (u *UI) askRestore() {
	u.dlg = &dialogRequest{
		title:   "Restore the default settings?",
		body:    restoreBody,
		okLabel: setRestore,
		prose:   true,
		reply:   make(chan string, 1),
		then: func(ans string) {
			if ans == "yes" {
				u.restoreDefaults()
			}
		},
	}
}

// restoreDefaults clears every key but the names and the music folder
// (Config.Restored) and puts the live window back to match: the palette,
// the player, the ticks, the backend's folder and line.
func (u *UI) restoreDefaults() {
	old := u.cfg
	u.cfg = old.Restored()

	u.applyTheme(ThemeSystem)
	u.audio.SetReplayGain(true)
	u.vol.muted, u.vol.dirty = false, false
	u.initVolume()

	u.st.FixNames, u.st.Organize, u.st.FixLowRes, u.st.DiscFolders = true, true, true, false
	u.namesTick.Value, u.organizeTick.Value, u.lowResTick.Value, u.discTick.Value = true, true, true, false

	if ls := u.live(); ls != nil {
		ls.SetBackupDir("")
		ls.SetMinArtPx(0)
		ls.SetOnlineArt(true)
		ls.SetArtistPictures(false)
		ls.SetCheckDuplicates(true)
		ls.SetPrereleases(false)
		ls.SetKeepBackups(0)
	}
	u.win.dirty = false
	u.savedTab = u.st.Tab
	if s := u.settings; s != nil {
		s.rgTick.Value, s.winTick.Value, s.tabTick.Value = true, true, true
		s.onlineTick.Value, s.dupTick.Value = true, true
		s.artistTick.Value = false
		s.verifyTick.Value, s.ejectTick.Value, s.preTick.Value = false, false, false
		s.playlistsEd.SetText("")
		s.genreEd.SetText("")
		s.logEd.SetText("")
	}
	u.saveConfig()
	u.st.Logf("settings: back to the defaults")

	if old.DiscFolders || old.MinArtPx != 0 || !old.CheckDuplicatesOn() {
		u.rescanSoon()
	}
	if old.BackupDir != "" && u.be != nil {
		serial := u.st.Device.Serial
		go func() {
			u.readBackups(context.Background(), serial)
			u.invalidate()
		}()
	}
}

// --- events ---------------------------------------------------------------

// settingsEvents is the sheet's half of the frame.
func (u *UI) settingsEvents(gtx C) {
	s := u.settings
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if ke, ok := ev.(key.Event); ok && ke.State == key.Press {
			u.closeSettings()
			return
		}
	}
	if s.closeBtn.Clicked(gtx) || s.scrim.Clicked(gtx) {
		u.closeSettings()
		return
	}
	s.block.Clicked(gtx) // a click on the plate is not a click outside it
	busy := u.st.Busy()

	u.tickEvents(gtx, busy)
	u.sourceEvents(gtx)
	if u.browseBtn.Clicked(gtx) && !busy {
		u.chooseMusicFolder()
	}
	for i := range s.artClicks {
		if s.artClicks[i].Clicked(gtx) {
			u.setMinArtPx(minArtChoices[i].px)
		}
	}
	if s.backupBrowse.Clicked(gtx) && !busy {
		u.chooseBackupFolder()
	}
	if s.backupShow.Clicked(gtx) {
		u.openDir(u.backupDirShown())
	}
	if s.rgTick.Update(gtx) {
		u.setReplayGain(s.rgTick.Value)
	}
	for i := range s.themeClicks {
		if s.themeClicks[i].Clicked(gtx) {
			u.setTheme(themeChoices[i])
		}
	}
	if s.winTick.Update(gtx) {
		u.setWindowRemember(s.winTick.Value)
	}
	if s.tabTick.Update(gtx) {
		u.setTabRemember(s.tabTick.Value)
	}
	if s.cfgShow.Clicked(gtx) {
		u.openDir(filepath.Dir(u.configPathShown()))
	}
	u.behaviourEvents(gtx, busy)
	if s.restoreBtn.Clicked(gtx) {
		u.askRestore()
	}
}

// behaviourEvents is the C3 rows' half of the frame.
func (u *UI) behaviourEvents(gtx C, busy bool) {
	s := u.settings
	for _, f := range []struct {
		ed  *widget.Editor
		dst *string
	}{{&s.playlistsEd, &u.cfg.PlaylistsDir}, {&s.genreEd, &u.cfg.GenreMap}, {&s.logEd, &u.cfg.LogFile}} {
		for {
			ev, ok := f.ed.Update(gtx)
			if !ok {
				break
			}
			switch ev.(type) {
			case widget.ChangeEvent, widget.SubmitEvent:
				u.setPath(f.dst, f.ed.Text())
			}
		}
	}
	if s.playlistsBrowse.Clicked(gtx) && !busy {
		u.choosePath(pickPlaylists)
	}
	if s.playlistsDefault.Clicked(gtx) {
		s.playlistsEd.SetText("")
		u.setPath(&u.cfg.PlaylistsDir, "")
	}
	if s.genreBrowse.Clicked(gtx) && !busy {
		u.choosePath(pickGenreMap)
	}
	if s.genreBuiltin.Clicked(gtx) {
		s.genreEd.SetText("")
		u.setPath(&u.cfg.GenreMap, "")
	}
	if s.logBrowse.Clicked(gtx) {
		u.choosePath(pickLogFolder)
	}
	if s.logShow.Clicked(gtx) && u.cfg.LogFile != "" {
		u.openDir(filepath.Dir(u.cfg.LogFile))
	}
	if s.onlineTick.Update(gtx) {
		u.setOnlineArt(s.onlineTick.Value)
	}
	if s.artistTick.Update(gtx) {
		u.setArtistPictures(s.artistTick.Value)
	}
	if s.dupTick.Update(gtx) {
		u.setCheckDuplicates(s.dupTick.Value)
	}
	if s.verifyTick.Update(gtx) {
		u.cfg.VerifySync = s.verifyTick.Value
		u.saveConfig()
	}
	if s.ejectTick.Update(gtx) {
		u.cfg.EjectAfterSync = s.ejectTick.Value
		u.saveConfig()
	}
	if s.preTick.Update(gtx) {
		u.setPrereleases(s.preTick.Value)
	}
	for i := range s.keepClicks {
		if s.keepClicks[i].Clicked(gtx) {
			u.setKeepBackups(keepChoices[i].n)
		}
	}
	for i := range s.updClicks {
		if s.updClicks[i].Clicked(gtx) && u.cfg.UpdateCheck != updateChoices[i].value {
			u.cfg.UpdateCheck = updateChoices[i].value
			u.saveConfig()
		}
	}
}

// --- drawing --------------------------------------------------------------

// settingsW is the sheet's width: the album sheet's, plus room for the
// four-stop picker beside a 150 dp label.
const settingsW = 660

// settingsLayer is the scrim and the plate in the middle of it.
func (u *UI) settingsLayer(gtx C) D {
	s := u.settings
	full := gtx.Constraints.Max
	paint.FillShape(gtx.Ops, u.pal.Scrim, clip.Rect{Max: full}.Op())
	s.scrim.Layout(gtx, func(gtx C) D { return D{Size: full} })

	w := full.X * 94 / 100
	if m := gtx.Dp(settingsW); w > m {
		w = m
	}
	maxH := full.Y * 88 / 100
	g := gtx
	g.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, maxH)}
	m := op.Record(gtx.Ops)
	dims := u.settingsBody(g)
	body := m.Stop()

	sz := image.Pt(w, dims.Size.Y)
	off := image.Pt((full.X-sz.X)/2, (full.Y-sz.Y)/2)
	defer op.Offset(off).Push(gtx.Ops).Pop()
	r := gtx.Dp(8)
	box := image.Rectangle{Max: sz}
	paint.FillShape(gtx.Ops, u.pal.Border, clip.UniformRRect(box, r).Op(gtx.Ops))
	paint.FillShape(gtx.Ops, u.pal.Plate, clip.UniformRRect(box.Inset(gtx.Dp(1)), r-gtx.Dp(1)).Op(gtx.Ops))
	bg := gtx
	bg.Constraints = layout.Exact(sz)
	s.block.Layout(bg, func(gtx C) D { return D{Size: sz} })
	body.Add(gtx.Ops)
	return D{Size: full}
}

// settingsBody is the title, the list (scrolling in whatever height the
// window leaves) and the foot: Restore defaults on the left, Close on the
// right.
func (u *UI) settingsBody(gtx C) D {
	in := gtx.Dp(18)
	gap := gtx.Dp(12)
	innerW := gtx.Constraints.Max.X - 2*in
	g := gtx
	g.Constraints = layout.Constraints{Min: image.Pt(innerW, 0), Max: image.Pt(innerW, gtx.Constraints.Max.Y)}

	hm := op.Record(gtx.Ops)
	head := func(gtx C) D {
		l := material.Label(u.th, unit.Sp(15), setTitle)
		l.Font.Weight = font.Bold
		l.Color = u.pal.Ink
		return l.Layout(gtx)
	}(g)
	headC := hm.Stop()
	fm := op.Record(gtx.Ops)
	foot := u.settingsFoot(g)
	footC := fm.Stop()

	room := gtx.Constraints.Max.Y - 2*in - head.Size.Y - foot.Size.Y - 2*gap
	if room < gtx.Dp(60) {
		room = gtx.Dp(60)
	}
	lg := g
	lg.Constraints = layout.Constraints{Min: image.Pt(innerW, 0), Max: image.Pt(innerW, room)}
	lm := op.Record(gtx.Ops)
	list := u.settingsList(lg)
	listC := lm.Stop()

	y := in
	place := func(c op.CallOp, h int) {
		st := op.Offset(image.Pt(in, y)).Push(gtx.Ops)
		c.Add(gtx.Ops)
		st.Pop()
		y += h
	}
	place(headC, head.Size.Y)
	y += gap
	place(listC, list.Size.Y)
	y += gap
	place(footC, foot.Size.Y)
	return D{Size: image.Pt(gtx.Constraints.Max.X, y+in)}
}

// settingsList is the sections, one list item each, and the file line.
func (u *UI) settingsList(gtx C) D {
	s := u.settings
	items := []layout.Widget{
		u.setHeading(setLibrary, setLibrarySub), u.libraryRows,
		u.setHeading(setSync, setSyncSub), u.syncRows,
		u.setHeading(setFirmware, setFirmwareSub), u.firmwareRows,
		u.setHeading(setPlayback, setPlaybackSub), u.playbackRows,
		u.setHeading(setAppearance, setAppearanceSub), u.appearanceRows,
		u.setHeading(setDiagnostics, setDiagnosticsSub), u.diagnosticsRows,
		u.settingsFile,
	}
	list := material.List(u.th, &s.list)
	list.Indicator.Color = u.pal.SelSub
	list.Indicator.HoverColor = u.pal.Muted2
	return list.Layout(gtx, len(items), func(gtx C, i int) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		// The scrollbar's lane: the rows stop short of it.
		return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, items[i])
	})
}

// setHeading is one section's name over its rows, with the sentence that
// says what the section is for. The name is in Ink, a size up from the
// rows, and the air above it is what separates one section from the last:
// six headings in the rows' own muted type blurred into them (the first
// Windows bench, docs/design/bench-2026-11-03.md).
func (u *UI) setHeading(name, sub string) layout.Widget {
	return func(gtx C) D {
		return layout.Inset{Top: unit.Dp(16), Bottom: unit.Dp(6), Left: unit.Dp(2)}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					l := material.Label(u.th, unit.Sp(13), name)
					l.Font.Weight = font.SemiBold
					l.Color = u.pal.Ink
					return l.Layout(gtx)
				}),
				layout.Rigid(func(gtx C) D {
					return layout.Inset{Top: unit.Dp(2)}.Layout(gtx, u.wrapN(11, u.pal.Muted, sub, 2))
				}),
			)
		})
	}
}

// setRows is a section's rows on one plate, hairlines between them: the
// Summary tab's rowsPlate, for rows that are widgets rather than data.
func (u *UI) setRows(gtx C, rows ...layout.Widget) D {
	return u.plateR(gtx, 7, func(gtx C) D {
		var children []layout.FlexChild
		for i, r := range rows {
			if i > 0 {
				children = append(children, layout.Rigid(u.hairline))
			}
			children = append(children, layout.Rigid(r))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

// valueSub is a row body: a value line and a muted sub-line under it.
func (u *UI) valueSub(value layout.Widget, sub string) layout.Widget {
	return func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(value),
			layout.Rigid(func(gtx C) D {
				if sub == "" {
					return D{}
				}
				// Three lines at most: the low-resolution row's sub-line
				// says what the line is for and what the iPod draws, and
				// cut short it would not.
				return layout.Inset{Top: unit.Dp(2)}.Layout(gtx, u.wrapN(11, u.pal.Muted2, sub, 3))
			}),
		)
	}
}

func (u *UI) libraryRows(gtx C) D {
	busy := u.st.Busy()
	return u.setRows(gtx,
		func(gtx C) D {
			return u.rowFrame(gtx, setMusicFolder, func(gtx C) D { return u.sourceRow(gtx, busy) }, nil)
		},
		func(gtx C) D {
			s := u.settings
			return u.rowFrame(gtx, setPlaylists, u.valueSub(func(gtx C) D {
				return u.pathField(gtx, &s.playlistsEd, setDefault,
					btn{&s.playlistsBrowse, setBrowse, false, !busy},
					btn{&s.playlistsDefault, setDefault, false, u.cfg.PlaylistsDir != ""})
			}, u.playlistsSub()), nil)
		},
		func(gtx C) D {
			return u.rowFrame(gtx, setFixAll, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx C) D { return u.setTick(gtx, &u.namesTick, setNames) }),
					layout.Rigid(func(gtx C) D { return u.setTick(gtx, &u.organizeTick, setOrganize) }),
					layout.Rigid(func(gtx C) D { return u.setTick(gtx, &u.lowResTick, setLowRes) }),
					layout.Rigid(func(gtx C) D { return u.setTick(gtx, &u.discTick, setDiscs) }),
				)
			}, nil)
		},
		func(gtx C) D {
			s := u.settings
			sel := minArtIndex(u.cfg.MinArtPx)
			labels := make([]string, len(minArtChoices))
			for i, c := range minArtChoices {
				labels[i] = c.label
			}
			return u.rowFrame(gtx, setSmallCovers, u.valueSub(func(gtx C) D {
				return u.radios(gtx, s.artClicks[:], labels, sel)
			}, u.minArtNote()), nil)
		},
		func(gtx C) D {
			s := u.settings
			return u.rowFrame(gtx, setCovers, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(u.valueSub(func(gtx C) D {
						return u.setTick(gtx, &s.onlineTick, setOnline)
					}, setOnlineSub)),
					layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
					layout.Rigid(u.valueSub(func(gtx C) D {
						return u.setTick(gtx, &s.artistTick, setArtistTick)
					}, u.artistSub())),
				)
			}, nil)
		},
		func(gtx C) D {
			s := u.settings
			return u.rowFrame(gtx, setDuplicates, u.valueSub(func(gtx C) D {
				return u.setTick(gtx, &s.dupTick, setDupTick)
			}, setDupSub), nil)
		},
	)
}

// syncRows is the Sync section: how copies are compared, the eject after,
// and the genre map. None of it changes what a sync writes (rule 5).
func (u *UI) syncRows(gtx C) D {
	s := u.settings
	busy := u.st.Busy()
	return u.setRows(gtx,
		func(gtx C) D {
			return u.rowFrame(gtx, setCopies, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(u.valueSub(func(gtx C) D { return u.setTick(gtx, &s.verifyTick, setVerify) },
						setVerifySub)),
					layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
					layout.Rigid(u.valueSub(func(gtx C) D { return u.setTick(gtx, &s.ejectTick, setEject) },
						setEjectSub)),
				)
			}, nil)
		},
		func(gtx C) D {
			return u.rowFrame(gtx, setGenreMap, u.valueSub(func(gtx C) D {
				return u.pathField(gtx, &s.genreEd, setBuiltin,
					btn{&s.genreBrowse, setBrowse, false, !busy},
					btn{&s.genreBuiltin, setBuiltin, false, u.cfg.GenreMap != ""})
			}, setGenreSub), nil)
		},
	)
}

// diagnosticsRows is the log file.
func (u *UI) diagnosticsRows(gtx C) D {
	s := u.settings
	return u.setRows(gtx, func(gtx C) D {
		return u.rowFrame(gtx, setLogFile, u.valueSub(func(gtx C) D {
			return u.pathField(gtx, &s.logEd, "None",
				btn{&s.logBrowse, setBrowse, false, true},
				btn{&s.logShow, setShowFolder, false, u.cfg.LogFile != ""})
		}, setLogSub), nil)
	})
}

// pathField is a path row's body: the field, then its buttons.
func (u *UI) pathField(gtx C, ed *widget.Editor, hint string, bs ...btn) D {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx C) D { return u.field(gtx, ed, hint) }),
		layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
		layout.Rigid(func(gtx C) D { return u.buttonRow(gtx, bs...) }),
	)
}

func (u *UI) firmwareRows(gtx C) D {
	s := u.settings
	busy := u.st.Busy()
	sub := setBackupSub
	if u.cfg.BackupDir != "" {
		sub = setBackupSub2
	}
	// The path has the row's whole width and the buttons go under it: a
	// backup folder is a long path, and one cut short is a folder nobody
	// can find.
	keepLabels := make([]string, len(keepChoices))
	for i, c := range keepChoices {
		keepLabels[i] = c.label
	}
	updLabels := make([]string, len(updateChoices))
	updSel := 0
	for i, c := range updateChoices {
		updLabels[i] = c.label
		if c.value == u.cfg.UpdateCheck {
			updSel = i
		}
	}
	return u.setRows(gtx, func(gtx C) D {
		return u.rowFrame(gtx, setBackupDir, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					l := material.Label(u.th, unit.Sp(11.5), u.backupDirShown())
					l.Font = Mono
					l.Color = u.pal.Ink
					l.MaxLines = 2
					return l.Layout(gtx)
				}),
				layout.Rigid(func(gtx C) D {
					return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(6)}.Layout(gtx, u.line(11, u.pal.Muted2, sub))
				}),
				layout.Rigid(func(gtx C) D {
					return u.buttonRow(gtx,
						btn{&s.backupBrowse, setBrowse, false, !busy},
						btn{&s.backupShow, setShowFolder, false, u.backupDirShown() != ""})
				}),
			)
		}, nil)
	},
		func(gtx C) D {
			return u.rowFrame(gtx, setKeep, u.valueSub(func(gtx C) D {
				return u.radios(gtx, s.keepClicks[:], keepLabels, keepIndex(u.cfg.KeepBackups))
			}, u.keepNote()), nil)
		},
		func(gtx C) D {
			return u.rowFrame(gtx, setUpdates, u.valueSub(func(gtx C) D {
				return u.radios(gtx, s.updClicks[:], updLabels, updSel)
			}, setUpdatesSub), nil)
		},
		func(gtx C) D {
			return u.rowFrame(gtx, setPreRow, u.valueSub(func(gtx C) D {
				return u.setTick(gtx, &s.preTick, setPre)
			}, setPreSub), nil)
		},
	)
}

func (u *UI) playbackRows(gtx C) D {
	s := u.settings
	return u.setRows(gtx, func(gtx C) D {
		return u.rowFrame(gtx, setRG, u.valueSub(func(gtx C) D {
			return u.setTick(gtx, &s.rgTick, setRGTick)
		}, setRGSub), nil)
	})
}

func (u *UI) appearanceRows(gtx C) D {
	s := u.settings
	return u.setRows(gtx,
		func(gtx C) D {
			sel := -1
			labels := make([]string, len(themeChoices))
			for i, t := range themeChoices {
				labels[i] = u.themeLabel(t)
				if t == u.theme {
					sel = i
				}
			}
			return u.rowFrame(gtx, setThemeRow, u.valueSub(func(gtx C) D {
				return u.radios(gtx, s.themeClicks[:], labels, sel)
			}, setThemeSub), nil)
		},
		func(gtx C) D {
			return u.rowFrame(gtx, setWindow, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx C) D { return u.setTick(gtx, &s.winTick, setWinTick) }),
					layout.Rigid(func(gtx C) D { return u.setTick(gtx, &s.tabTick, setTabTick) }),
				)
			}, nil)
		},
	)
}

// themeLabel is a theme as the radio says it; System says what it is now.
// What System resolves to is read once while the sheet is open (it is a
// registry read on Windows; not one per frame).
func (u *UI) themeLabel(t Theme) string {
	if t != ThemeSystem {
		return t.String()
	}
	if u.systemNow == "" {
		u.systemNow = ThemeSystem.Resolve()
	}
	return "System — " + u.systemNow.String() + " now"
}

// settingsFile is the end of the list: where the file is, and the one
// sentence that says what is not a setting.
func (u *UI) settingsFile(gtx C) D {
	s := u.settings
	return layout.Inset{Top: unit.Dp(12), Left: unit.Dp(2)}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
					layout.Flexed(1, func(gtx C) D {
						l := material.Label(u.th, unit.Sp(11), setFileIs+u.configPathShown())
						l.Color = u.pal.Muted2
						l.MaxLines = 1
						l.Truncator = "…"
						return l.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
					layout.Rigid(func(gtx C) D { return u.link(gtx, &s.cfgShow, setShowFolder, 11) }),
				)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
			layout.Rigid(u.wrapN(11, u.pal.Muted2, setFixed, 2)),
		)
	})
}

// settingsFoot is Restore defaults and Close.
func (u *UI) settingsFoot(gtx C) D {
	s := u.settings
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx C) D { return u.button(gtx, btn{&s.restoreBtn, setRestore, false, true}) }),
		layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Max.X, 0)} }),
		layout.Rigid(func(gtx C) D { return u.button(gtx, btn{&s.closeBtn, setClose, false, true}) }),
	)
}

// setTick is one tick box and its words, the Fix tab's check box (the
// same widget.Bool, where the tick is shared) at the sheet's size.
func (u *UI) setTick(gtx C, b *widget.Bool, label string) D {
	cb := material.CheckBox(u.th, b, label)
	cb.Color = u.pal.Ink
	cb.IconColor = u.pal.Ink
	cb.Size = unit.Dp(16)
	cb.TextSize = unit.Sp(12.5)
	return cb.Layout(gtx)
}

// radios is a row of radio buttons: the menu's radio dot and the words,
// side by side, the chosen one in SemiBold. sel < 0 chooses none.
func (u *UI) radios(gtx C, clicks []widget.Clickable, labels []string, sel int) D {
	var children []layout.FlexChild
	for i := range labels {
		i := i
		if i > 0 {
			children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(14)}.Layout))
		}
		children = append(children, layout.Rigid(func(gtx C) D {
			return clicks[i].Layout(gtx, func(gtx C) D {
				d := layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx C) D { return u.radioDot(gtx, i == sel) }),
						layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
						layout.Rigid(func(gtx C) D {
							l := material.Label(u.th, unit.Sp(12.5), labels[i])
							l.Color = u.pal.Ink
							l.MaxLines = 1
							if i == sel {
								l.Font.Weight = font.SemiBold
							}
							return l.Layout(gtx)
						}),
					)
				})
				defer clip.Rect{Max: d.Size}.Push(gtx.Ops).Pop()
				pointer.CursorPointer.Add(gtx.Ops)
				return d
			})
		}))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}
