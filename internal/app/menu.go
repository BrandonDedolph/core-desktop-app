package app

import (
	"image"
	"io"
	"os/exec"
	"runtime"
	"strings"

	"gioui.org/font"
	"gioui.org/io/clipboard"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/BrandonDedolph/core-desktop-app/internal/version"
)

// The "···" menu: everything the header has no room for and nobody needs
// every day. Gio has no menu widget, so this is ours: a plate hung under the
// button, over a click-catcher that covers the window (a click outside
// closes it, as Esc does), drawn in the same top layer as the dialogs.

// The items, in the order they are drawn.
const (
	menuBackup = iota
	menuFlashFile
	menuReread
	menuCheck
	menuShowLog
	menuCopyLog
	menuOpenMusic
	menuGetApp
	menuSettings
	menuAbout
	menuCount
)

// menuLabels are the words, verb first — with two nouns that are the
// accepted exceptions, About and Settings…: everything that is a standing
// choice rather than an action lives behind Settings… (the settings plan's
// Rule 4: verbs in the menu, nouns in Settings).
var menuLabels = [menuCount]string{
	menuBackup:    "Back up the firmware",
	menuFlashFile: "Flash an image file…",
	menuReread:    "Read the iPod again",
	menuCheck:     "Check for updates",
	menuShowLog:   "Show the log",
	menuCopyLog:   "Copy the log",
	menuOpenMusic: "Open the music folder",
	menuGetApp:    "Update the app",
	menuSettings:  "Settings…",
	menuAbout:     "About",
}

// menuGroups puts a hairline between the firmware writes, the reads, the
// log and folder group, and the app's own (Settings…, About).
var menuGroups = [][]int{
	{menuBackup, menuFlashFile},
	{menuReread, menuCheck},
	{menuShowLog, menuCopyLog, menuOpenMusic},
	{menuGetApp, menuSettings, menuAbout},
}

// menuEnabled is each item's condition. A greyed item says the thing
// exists and is not available now, which is more use than an item that
// disappears while a job runs.
func (u *UI) menuEnabled(i int) bool {
	busy := u.st.Busy()
	switch i {
	case menuBackup:
		return !busy && u.st.Device.Found
	case menuFlashFile:
		return !busy && u.st.Device.Found
	case menuReread, menuCheck, menuGetApp:
		return !busy
	case menuCopyLog:
		return len(u.st.Log) > 0
	case menuOpenMusic:
		return strings.TrimSpace(u.st.Source) != ""
	}
	return true
}

// menuVisible hides the one item that only exists sometimes: "Update the
// app to vX.Y.Z", there only when the launch check found a newer app.
func (u *UI) menuVisible(i int) bool {
	if i == menuGetApp {
		return u.st.NewerApp != ""
	}
	return true
}

// menuEvents is the menu's half of the frame.
func (u *UI) menuEvents(gtx C) {
	if !u.st.MenuOpen {
		return
	}
	if u.menuScrim.Clicked(gtx) {
		u.st.MenuOpen = false
		return
	}
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape})
		if !ok {
			break
		}
		if ke, ok := ev.(key.Event); ok && ke.State == key.Press {
			u.st.MenuOpen = false
			return
		}
	}
	for i := range u.menuClicks {
		if !u.menuClicks[i].Clicked(gtx) || !u.menuEnabled(i) || !u.menuVisible(i) {
			continue
		}
		u.st.MenuOpen = false
		u.menuDo(gtx, i)
		return
	}
}

// menuDo runs one item.
func (u *UI) menuDo(gtx C, i int) {
	switch i {
	case menuSettings:
		u.openSettings()
	case menuBackup:
		u.startBackup()
	case menuFlashFile:
		u.pick(JobFlash)
	case menuReread:
		u.startRefresh()
	case menuCheck:
		u.startCheck()
	case menuShowLog:
		u.st.LogOpen = !u.st.LogOpen
	case menuCopyLog:
		u.copyLog(gtx)
	case menuOpenMusic:
		u.openMusicFolder()
	case menuGetApp:
		u.startAppUpdate()
	case menuAbout:
		u.showAbout()
	}
}

// setTheme applies a palette choice now and remembers it.
func (u *UI) setTheme(t Theme) {
	u.applyTheme(t)
	u.cfg.Theme = t
	u.saveConfig()
}

// copyLog puts the whole log on the clipboard.
func (u *UI) copyLog(gtx C) {
	text := strings.Join(u.st.Log, "\n") + "\n"
	gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(text))})
	u.st.Logf("copied %d log lines to the clipboard", len(u.st.Log))
}

// openFolder is the OS file manager on a folder. A variable so a test can
// see what would have been opened without opening anything.
var openFolder = func(dir string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap it: explorer exits 1 even when it worked, so the status is
	// not worth reading.
	go func() { _ = cmd.Wait() }()
	return nil
}

// openMusicFolder shows the music folder in the file manager, or does
// nothing when there is none.
func (u *UI) openMusicFolder() {
	src := strings.TrimSpace(u.st.Source)
	if src == "" {
		return
	}
	if err := openFolder(src); err != nil {
		u.st.Logf("could not open %s: %v", src, err)
	}
}

// showAbout is the About dialog: a read, closed with its one button.
func (u *UI) showAbout() {
	cli := u.st.CLIPath
	if cli == "" {
		cli = "not found beside this app"
	}
	u.dlg = &dialogRequest{
		title:   "About Core",
		body:    strings.Join(u.aboutLines(cli), "\n"),
		okLabel: "Close",
		info:    true,
		reply:   make(chan string, 1),
	}
}

// menuLayer draws the open menu: the click-catcher over the whole window,
// then the plate hung from the right of the top bar.
func (u *UI) menuLayer(gtx C) D {
	full := gtx.Constraints.Max
	u.menuScrim.Layout(gtx, func(gtx C) D { return D{Size: full} })

	w := gtx.Dp(236)
	x := full.X - gtx.Dp(14) - w
	y := u.topH - gtx.Dp(6)
	if y < 0 {
		y = 0
	}
	defer op.Offset(image.Pt(x, y)).Push(gtx.Ops).Pop()
	gtx.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, full.Y-y)}
	return u.plate(gtx, func(gtx C) D {
		return layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5)}.Layout(gtx, func(gtx C) D {
			var rows []layout.FlexChild
			for g, items := range menuGroups {
				if g > 0 {
					rows = append(rows, layout.Rigid(func(gtx C) D {
						return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, u.hairline)
					}))
				}
				for _, i := range items {
					i := i
					if !u.menuVisible(i) {
						continue
					}
					rows = append(rows, layout.Rigid(func(gtx C) D {
						label := menuLabels[i]
						if i == menuShowLog && u.st.LogOpen {
							label = "Hide the log"
						}
						if i == menuGetApp {
							label = appUpdateLabel(u.st.NewerApp)
						}
						return u.menuRow(gtx, &u.menuClicks[i], label, u.menuEnabled(i), nil, false)
					}))
				}
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
		})
	})
}

// menuRow is one clickable line. mark, when there is one, is drawn before
// the words; on sets the words in SemiBold. (The theme's radio rows and
// ReplayGain's tick box used both; they live in Settings now, and the
// parameters stay for the next item that is a state rather than an act.)
func (u *UI) menuRow(gtx C, click *widget.Clickable, label string, enabled bool, mark layout.Widget, on bool) D {
	if !enabled {
		gtx = gtx.Disabled()
	}
	col := u.pal.Ink
	if !enabled {
		col = u.pal.Muted2
	}
	return click.Layout(gtx, func(gtx C) D {
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx C) D {
				sz := gtx.Constraints.Min
				if enabled {
					defer clip.Rect{Max: sz}.Push(gtx.Ops).Pop()
					pointer.CursorPointer.Add(gtx.Ops)
					if click.Hovered() {
						paint.FillShape(gtx.Ops, u.pal.Trk, clip.Rect{Max: sz}.Op())
					}
				}
				return D{Size: sz}
			}),
			layout.Stacked(func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(12), Right: unit.Dp(12)}.
					Layout(gtx, func(gtx C) D {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx C) D {
								if mark == nil {
									return D{}
								}
								return layout.Inset{Right: unit.Dp(8)}.Layout(gtx, mark)
							}),
							layout.Rigid(func(gtx C) D {
								l := material.Label(u.th, unit.Sp(13), label)
								l.Color = col
								l.MaxLines = 1
								if on {
									l.Font.Weight = font.SemiBold
								}
								return l.Layout(gtx)
							}),
						)
					})
			}),
		)
	})
}

// radioDot is a 10 dp ring, with an ink centre when on.
func (u *UI) radioDot(gtx C, on bool) D {
	s := gtx.Dp(10)
	sz := image.Pt(s, s)
	paint.FillShape(gtx.Ops, u.pal.Muted2, clip.Ellipse{Max: sz}.Op(gtx.Ops))
	w := gtx.Dp(1)
	paint.FillShape(gtx.Ops, u.pal.Plate, clip.Ellipse{Min: image.Pt(w, w), Max: sz.Sub(image.Pt(w, w))}.Op(gtx.Ops))
	if on {
		i := gtx.Dp(2.5)
		paint.FillShape(gtx.Ops, u.pal.Ink, clip.Ellipse{Min: image.Pt(i, i), Max: sz.Sub(image.Pt(i, i))}.Op(gtx.Ops))
	}
	return D{Size: sz}
}

// aboutLines is the About dialog's text: this build, what firmware it
// writes for, the CLI beside it, a newer app when the launch check found
// one, and the licences.
func (u *UI) aboutLines(cli string) []string {
	lines := []string{
		"core-app " + version.Version,
		"commit " + version.Commit + "  ·  built " + version.Date,
		"writes files for Core " + MinFirmware + " or newer",
		"core CLI: " + cli,
	}
	if u.st.NewerApp != "" {
		lines = append(lines, "", "Core app "+u.st.NewerApp+" is available: ··· → "+appUpdateLabel(u.st.NewerApp)+
			" downloads it and restarts.")
		if u.st.NewerAppURL != "" {
			lines = append(lines, "The release: "+u.st.NewerAppURL)
		}
	}
	return append(lines, "", "Core is Apache-2.0 · Nunito is OFL")
}
