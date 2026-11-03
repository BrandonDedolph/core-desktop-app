package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/BrandonDedolph/core-desktop-app/internal/devicefs"
	"github.com/BrandonDedolph/core-desktop-app/internal/doctor"
)

// The drawer's second tab: the iPod's own log, CORELOG.BIN, which the
// firmware writes a block at a time and the window only ever reads. It is
// the device half of charter job 6 ("see and read the log from the desktop
// app and from the iPod"); before it, only tools/make_log.py in the
// firmware repo could show it.
//
// When it reads: when the tab comes into view (the drawer opening on it, or
// a click on it), after every Refresh (inside the Refresh job, which is
// read-only), and once more after a job that touched the iPod finishes while
// the tab is on screen. Never while such a job runs: a request then waits
// for the job to end, and the pane keeps what it last read.

// LogTab is one of the drawer's two tabs.
type LogTab int

// The tabs, in the order they are drawn.
const (
	LogTabWindow LogTab = iota
	LogTabIPod
)

// String names the tab as it is drawn.
func (t LogTab) String() string {
	if t == LogTabIPod {
		return "The iPod"
	}
	return "This window"
}

// What the iPod tab says when there is no log to show.
const (
	devLogNoIPod   = "No iPod"
	devLogEjected  = "The iPod is ejected"
	devLogNone     = "No log on the iPod yet"
	devLogReading  = "reading…"
	devLogReadFail = "The iPod's log could not be read: "
)

// devLogRead is one read of the log: the volume it came off, and its boots
// or why there are none.
type devLogRead struct {
	vol   string
	boots []doctor.BootLog
	err   error
}

// logLineKind is how a line of the iPod tab is drawn.
type logLineKind int

const (
	lineText logLineKind = iota // the device's own words, in mono
	lineBoot                    // a boot's divider
	lineNote                    // the window's remark: a gap, or the pane's state
)

type logLine struct {
	kind logLineKind
	text string
}

// touchesIPod is a job that writes to the iPod (its firmware, its volume,
// its label) or takes it away (Eject). The log is not read while one runs.
func touchesIPod(k JobKind) bool {
	switch k {
	case JobSync, JobSyncPrune, JobRename, JobEject:
		return true
	}
	return k.Writes()
}

// writerRunning is a job that touches the iPod in progress now.
func (u *UI) writerRunning() bool {
	return u.st.Job.Running() && touchesIPod(u.st.Job.Kind)
}

// requestDeviceLog reads the log off the UI goroutine, or — while a job
// that touches the iPod runs — notes that it is wanted when that job ends.
// It is not a Runner job: a library scan can take a minute, and the tab
// should not wait behind it, nor write "--- read ---" into the window's log.
func (u *UI) requestDeviceLog() {
	vol := u.st.Device.Volume
	if vol == "" || u.st.Ejected || u.be == nil {
		return
	}
	if u.writerRunning() {
		u.devLogQueued = true
		return
	}
	u.devLogQueued = false
	u.devLogReads.Add(1)
	go func() {
		defer u.devLogReads.Done()
		u.readDeviceLog(context.Background(), vol)
	}()
}

// readDeviceLog is the read itself, published under the mutex like every
// result from off the UI goroutine. The Refresh job calls it directly.
func (u *UI) readDeviceLog(ctx context.Context, vol string) {
	boots, err := u.be.ReadDeviceLog(ctx, vol)
	u.mu.Lock()
	u.newDevLog = &devLogRead{vol: vol, boots: boots, err: err}
	u.mu.Unlock()
	u.invalidate()
}

// devLogAfter is drain's hook for each event: a job that touched the iPod
// has ended while the tab is on screen, so what the tab shows may be old.
func (u *UI) devLogAfter(e Event) {
	if (e.Kind == EventDone || e.Kind == EventError) && touchesIPod(e.Job) && u.devLogShown {
		u.devLogQueued = true
	}
}

// devLogFrame runs once a frame after the drawer's clicks: the tab coming
// into view reads, and a read that was waiting for a job goes once the job
// is over.
func (u *UI) devLogFrame() {
	shown := u.st.LogOpen && u.st.LogTab == LogTabIPod
	if shown && !u.devLogShown {
		u.requestDeviceLog()
	}
	u.devLogShown = shown
	if u.devLogQueued && !u.writerRunning() {
		u.requestDeviceLog()
	}
}

// devLogLines is the iPod tab's content: the boots newest first, each under
// a divider naming its build and its start, its lines oldest first; or one
// note saying why there is nothing.
func (u *UI) devLogLines() []logLine {
	note := func(s string) []logLine { return []logLine{{lineNote, s}} }
	vol := u.st.Device.Volume
	switch {
	case vol == "" || !u.st.Device.Found:
		return note(devLogNoIPod)
	case u.st.Ejected:
		return note(devLogEjected)
	}
	r := u.devLog
	if r == nil || r.vol != vol {
		return note(devLogReading)
	}
	switch {
	case errors.Is(r.err, fs.ErrNotExist):
		return note(devLogNone)
	case r.err != nil:
		return note(devLogReadFail + r.err.Error())
	case len(r.boots) == 0:
		return note(devLogNone)
	}
	var out []logLine
	for _, b := range r.boots {
		out = append(out, logLine{lineBoot, bootDivider(b.Facts, u.loc)})
		for _, e := range b.Entries {
			if e.Missing > 0 {
				out = append(out, logLine{lineNote, fmt.Sprintf(
					"… %s missing here — overwritten by the ring, or torn", blocks(e.Missing))})
			}
			text := strings.TrimSuffix(e.Text, "\n")
			if text == "" {
				continue
			}
			for _, l := range strings.Split(text, "\n") {
				out = append(out, logLine{lineText, l})
			}
		}
	}
	return out
}

func blocks(n int64) string {
	if n == 1 {
		return "1 block"
	}
	return fmt.Sprintf("%d blocks", n)
}

// bootDivider is one boot's heading: its id, the build it said it was, and
// the time its clock read at the start — each as the log records it, and
// said plainly when the log does not.
func bootDivider(f doctor.BootFacts, loc *time.Location) string {
	parts := []string{fmt.Sprintf("Boot %d", f.ID)}
	switch {
	case f.Version == "":
		parts = append(parts, "build not recorded")
	case f.BuildID != "" && f.BuildID != f.Version:
		parts = append(parts, "Core "+f.Version+" ("+f.BuildID+")")
	default:
		parts = append(parts, "Core "+f.Version)
	}
	if f.At.IsZero() {
		parts = append(parts, "start time not recorded")
	} else {
		parts = append(parts, "started "+f.At.In(loc).Format("Jan 2 at 15:04"))
	}
	if !f.Start {
		parts = append(parts, "its first lines were overwritten")
	}
	return strings.Join(parts, " · ")
}

// devLogBoots is how many boots the tab shows, for the header's count and
// whether Copy has anything to copy.
func (u *UI) devLogBoots() int {
	if r := u.devLog; r != nil && r.err == nil && r.vol == u.st.Device.Volume &&
		u.st.Device.Found && !u.st.Ejected {
		return len(r.boots)
	}
	return 0
}

// shownLogText is what Copy puts on the clipboard: the tab on screen, as
// text. A boot's divider is set off the way the dump tool sets one off.
func (u *UI) shownLogText() string {
	if u.st.LogTab != LogTabIPod {
		return strings.Join(u.st.Log, "\n") + "\n"
	}
	var b strings.Builder
	for _, l := range u.devLogLines() {
		switch l.kind {
		case lineBoot:
			b.WriteString("=== " + l.text + " ===\n")
		default:
			b.WriteString(l.text + "\n")
		}
	}
	return b.String()
}

// devLogStrings is the tab's front, for the words test: the tab names,
// every state the pane can be in, and a divider of each shape.
func devLogStrings() []string {
	out := []string{LogTabWindow.String(), LogTabIPod.String(),
		devLogNoIPod, devLogEjected, devLogNone, devLogReading, devLogReadFail,
		"… " + blocks(3) + " missing here — overwritten by the ring, or torn"}
	at := time.Date(2026, 11, 2, 14, 3, 0, 0, time.UTC)
	for _, f := range []doctor.BootFacts{
		{ID: 12, Version: "v0.2.2", BuildID: "v0.2.2-5-g39fdaf9", At: at, Start: true},
		{ID: 3},
	} {
		out = append(out, bootDivider(f, time.UTC))
	}
	return out
}

// devLogPane draws the iPod tab's lines.
func (u *UI) devLogPane(gtx C) D {
	lines := u.devLogLines()
	list := material.List(u.th, &u.devLogList)
	list.Indicator.Color = u.pal.SelSub
	return list.Layout(gtx, len(lines), func(gtx C, i int) D {
		l := lines[i]
		switch l.kind {
		case lineBoot:
			top := unit.Dp(10)
			if i == 0 {
				top = 0
			}
			return layout.Inset{Top: top, Bottom: unit.Dp(3)}.Layout(gtx, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						if i == 0 {
							return D{}
						}
						return layout.Inset{Bottom: unit.Dp(6)}.Layout(gtx, u.hairline)
					}),
					layout.Rigid(func(gtx C) D {
						lb := material.Label(u.th, unit.Sp(11.5), l.text)
						lb.Color = u.pal.Ink
						lb.MaxLines = 1
						return lb.Layout(gtx)
					}),
				)
			})
		case lineNote:
			lb := material.Label(u.th, unit.Sp(11), l.text)
			lb.Color = u.pal.Muted
			lb.MaxLines = 1
			return lb.Layout(gtx)
		}
		lb := material.Label(u.th, unit.Sp(11), l.text)
		lb.Font = Mono
		lb.Color = u.pal.SelTrk
		lb.MaxLines = 1
		return lb.Layout(gtx)
	})
}

// cannedDeviceLog is the iPod tab's picture for `--dialog ipodlog`: two
// boots of the firmware's own narration, the older one cut by the ring.
func cannedDeviceLog(vol string) *devLogRead {
	at := time.Date(2026, 11, 2, 14, 3, 0, 0, time.UTC)
	text := func(s ...string) string { return strings.Join(s, "\n") + "\n" }
	return &devLogRead{vol: vol, boots: []doctor.BootLog{
		{
			Facts: doctor.BootFacts{ID: 12, Start: true, At: at, Version: "v0.2.2",
				BuildID: "v0.2.2-5-g39fdaf9"},
			Entries: []devicefs.LogEntry{{Seq: 40, Boot: 12, Text: text(
				"core: kernel alive (iPod 5G/5.5G, PP5022)",
				"core: version v0.2.2 build v0.2.2-5-g39fdaf9",
				"core: mount rc 00000000 BTUS 007A1200",
				"core: timesync host 6906E4C4 off FFFFFF88 applied 6906E4C4 rtc 00000000 -> set rc 00000000",
				"core: library songs 0000038B albums 00000065 ms 00000ABC",
				"core: evlog on seq 00000028 boot 0000000C prev final lba 00020000/00020010",
				"core: audio fifo_empty 00000000 rg_q8 FFFFFF40 capped 00000000",
				"core: suspend: entering",
			), Final: true}},
		},
		{
			Facts: doctor.BootFacts{ID: 11, Version: "v0.2.1", BuildID: "v0.2.1",
				At: at.Add(-26 * time.Hour)},
			Entries: []devicefs.LogEntry{{Seq: 37, Boot: 11, Missing: 2, Text: text(
				"core: batt raw 0312 mv 3987",
				"core: disk mode: entering",
			), Final: true}},
		},
	}}
}

func bootCount(n int) string { return fmt.Sprintf("%d boot%s", n, plural(n)) }
