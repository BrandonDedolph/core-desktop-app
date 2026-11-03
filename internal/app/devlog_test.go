package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BrandonDedolph/core-desktop-app/internal/devicefs"
	"github.com/BrandonDedolph/core-desktop-app/internal/doctor"
)

var devLogAt = time.Date(2026, 11, 2, 14, 3, 0, 0, time.UTC)

// twoBootRing is a CORELOG.BIN as the firmware leaves it after two boots:
// boot 7 on v0.2.1 that went to sleep, then boot 8 on v0.2.2, with the block
// between them torn. The lines are kernel/main.c's own formats.
func twoBootRing(t *testing.T) []byte {
	t.Helper()
	ts := func(host uint32) string {
		return fmt.Sprintf("core: timesync host %08X off FFFFFF88 applied %08X rtc 00000000 -> set rc 00000000",
			host, host)
	}
	const count = 8
	data := make([]byte, count*devicefs.LogBlockBytes)
	h := devicefs.EncodeLogHeader(count, 0x0BADCAFE)
	copy(data, h[:])
	for _, b := range []struct {
		seq   uint32
		boot  uint16
		text  string
		final bool
	}{
		{3, 7, "core: kernel alive (iPod 5G/5.5G, PP5022)\ncore: version v0.2.1 build v0.2.1\n" +
			ts(uint32(devLogAt.Add(-24*time.Hour).Unix())) + "\n", false},
		{4, 7, "core: suspend: entering\n", true},
		// seq 5 never made it: torn, so absent.
		{6, 8, "core: kernel alive (iPod 5G/5.5G, PP5022)\ncore: version v0.2.2 build v0.2.2-5-g39fdaf9\n" +
			ts(uint32(devLogAt.Unix())) + "\n", false},
		{7, 8, "play: Sigur R\xc3\xb3s - ()\n", false},
	} {
		blk, err := devicefs.EncodeLogBlock(b.seq, b.boot, []byte(b.text), b.final)
		if err != nil {
			t.Fatal(err)
		}
		copy(data[int(devicefs.LogBlockIndex(b.seq, count))*devicefs.LogBlockBytes:], blk[:])
	}
	return data
}

// twoBootLog is that ring as the real backend reads it off a volume.
func twoBootLog(t *testing.T) (vol string, boots []doctor.BootLog) {
	t.Helper()
	vol = t.TempDir()
	if err := os.WriteFile(filepath.Join(vol, devicefs.LogName), twoBootRing(t), 0o644); err != nil {
		t.Fatal(err)
	}
	boots, err := (&RealBackend{}).ReadDeviceLog(context.Background(), vol)
	if err != nil {
		t.Fatal(err)
	}
	return vol, boots
}

const twoBootText = `=== Boot 8 · Core v0.2.2 (v0.2.2-5-g39fdaf9) · started Nov 2 at 14:03 ===
… 1 block missing here — overwritten by the ring, or torn
core: kernel alive (iPod 5G/5.5G, PP5022)
core: version v0.2.2 build v0.2.2-5-g39fdaf9
core: timesync host 6AE89814 off FFFFFF88 applied 6AE89814 rtc 00000000 -> set rc 00000000
play: Sigur Rós - ()
=== Boot 7 · Core v0.2.1 · started Nov 1 at 14:03 ===
core: kernel alive (iPod 5G/5.5G, PP5022)
core: version v0.2.1 build v0.2.1
core: timesync host 6AE74694 off FFFFFF88 applied 6AE74694 rtc 00000000 -> set rc 00000000
core: suspend: entering
`

// devLogUI is a test window on the fake, the drawer closed, dividers in UTC.
func devLogUI(f *fakeBackend) *UI {
	u := newTestUI(f)
	u.loc = time.UTC
	return u
}

// frame lays out one frame and lets any read it started land.
func frame(u *UI) {
	u.Layout(newTestContext(900, 600))
	u.devLogReads.Wait()
	u.drain()
}

func openIPodTab(u *UI) {
	u.st.LogOpen = true
	u.logTabClicks[LogTabIPod].Click()
	frame(u)
}

// The pane's text for a two-boot ring, read off a volume by the real
// backend and handed to the window by the fake: newest boot first, each under
// its build and its start time, the torn block said in words, and the lines
// in the dump's order. Copy copies exactly this.
func TestIPodTabShowsTwoBoots(t *testing.T) {
	_, boots := twoBootLog(t)
	f := &fakeBackend{devLog: boots}
	u := devLogUI(f)
	openIPodTab(u)
	if u.st.LogTab != LogTabIPod {
		t.Fatal("the tab click did not choose The iPod")
	}
	if got := u.shownLogText(); got != twoBootText {
		t.Errorf("the iPod tab reads\n%s\nwant\n%s", got, twoBootText)
	}
	if len(f.devLogCalls) != 1 || f.devLogCalls[0] != `D:\` {
		t.Errorf("reads = %q, want one of D:\\", f.devLogCalls)
	}
	if n := u.devLogBoots(); n != 2 {
		t.Errorf("header count = %d boots", n)
	}
	// Copy copies the tab on screen, and says so in the window's log.
	u.copyBtn.Click()
	frame(u)
	if got := u.st.Log[len(u.st.Log)-1]; got != "copied the iPod's log (2 boots) to the clipboard" {
		t.Errorf("Copy logged %q", got)
	}
	u.logTabClicks[LogTabWindow].Click()
	frame(u)
	if got, want := u.shownLogText(), strings.Join(u.st.Log, "\n")+"\n"; got != want {
		t.Errorf("This window copies %q, want the window's log", got)
	}
}

// The tab says what state it is in, rather than showing an empty pane.
func TestIPodTabStates(t *testing.T) {
	cases := []struct {
		name string
		set  func(u *UI, f *fakeBackend)
		want string
	}{
		{"no iPod", func(u *UI, f *fakeBackend) { u.st.Device = Device{} }, devLogNoIPod},
		{"ejected", func(u *UI, f *fakeBackend) { u.st.Ejected = true }, devLogEjected},
		{"no file", func(u *UI, f *fakeBackend) { f.devLogErr = fs.ErrNotExist }, devLogNone},
		{"a valid ring with nothing flushed yet", func(u *UI, f *fakeBackend) {}, devLogNone},
		{"no valid header", func(u *UI, f *fakeBackend) { f.devLogErr = devicefs.ErrLogHeader },
			devLogReadFail + devicefs.ErrLogHeader.Error()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeBackend{}
			u := devLogUI(f)
			c.set(u, f)
			openIPodTab(u)
			if got := u.shownLogText(); got != c.want+"\n" {
				t.Errorf("pane = %q, want %q", got, c.want)
			}
			if u.devLogBoots() != 0 {
				t.Error("Copy is enabled with nothing to copy")
			}
		})
	}
	// Before the read lands it says so, and a read of another volume (the
	// iPod that was there before) is not shown as this one's.
	_, boots := twoBootLog(t)
	u := devLogUI(&fakeBackend{})
	u.st.LogOpen, u.st.LogTab = true, LogTabIPod
	if got := u.shownLogText(); got != devLogReading+"\n" {
		t.Errorf("before any read = %q", got)
	}
	u.devLog = &devLogRead{vol: `E:\`, boots: boots}
	if got := u.shownLogText(); got != devLogReading+"\n" {
		t.Errorf("another volume's log = %q", got)
	}
}

// When it reads: on coming into view (once, not every frame), and after every
// Refresh, which picks up what the iPod wrote since.
func TestIPodTabReadsOnOpenAndOnRefresh(t *testing.T) {
	_, boots := twoBootLog(t)
	f := &fakeBackend{dev: Device{Found: true, Volume: `D:\`, Path: `\\.\PhysicalDrive2`, Tested: true},
		devLog: boots[1:]}
	u := devLogUI(f)
	frame(u)
	if len(f.devLogCalls) != 0 {
		t.Fatalf("read with the drawer closed: %q", f.devLogCalls)
	}
	openIPodTab(u)
	frame(u)
	frame(u)
	if len(f.devLogCalls) != 1 {
		t.Fatalf("%d reads for one opening, want 1", len(f.devLogCalls))
	}
	if strings.Contains(u.shownLogText(), "Boot 8") {
		t.Fatal("boot 8 shown before the iPod wrote it")
	}

	// The iPod boots again; a Refresh reads it.
	f.mu.Lock()
	f.devLog = boots
	f.mu.Unlock()
	u.startRefresh()
	settle(u)
	frame(u)
	if len(f.devLogCalls) != 2 {
		t.Fatalf("%d reads after a Refresh, want 2", len(f.devLogCalls))
	}
	if got := u.shownLogText(); got != twoBootText {
		t.Errorf("after the Refresh the tab reads\n%s", got)
	}

	// Closing and reopening the drawer reads again, and the tab is still
	// The iPod: remembered for the session.
	u.logCloseBtn.Click()
	frame(u)
	u.st.LogOpen = true
	frame(u)
	if u.st.LogTab != LogTabIPod || len(f.devLogCalls) != 3 {
		t.Errorf("reopened: tab %v, %d reads", u.st.LogTab, len(f.devLogCalls))
	}
}

// Never while a job that writes to the iPod runs: opening the tab during a
// sync waits for it, and the read comes when the sync is over.
func TestIPodTabNeverReadsDuringASync(t *testing.T) {
	_, boots := twoBootLog(t)
	f := &fakeBackend{block: make(chan struct{}), devLog: boots}
	u := devLogUI(f)
	u.startSync(JobSync)
	for i := 0; i < 50 && !func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.inSync }(); i++ {
		time.Sleep(time.Millisecond)
	}
	openIPodTab(u)
	frame(u)
	if len(f.devLogCalls) != 0 {
		t.Fatalf("read during the sync: %q", f.devLogCalls)
	}
	if got := u.shownLogText(); got != devLogReading+"\n" {
		t.Errorf("during the sync the pane = %q", got)
	}
	close(f.block)
	settle(u)
	frame(u)
	f.mu.Lock()
	calls, during := len(f.devLogCalls), f.readInSync
	f.mu.Unlock()
	if during {
		t.Error("a read reached the backend while the sync ran")
	}
	if calls == 0 {
		t.Fatal("the waiting read never came after the sync")
	}
	if got := u.shownLogText(); got != twoBootText {
		t.Errorf("after the sync the tab reads\n%s", got)
	}
}

// The iPod tab never offers to clear the device's log, and Clear's handler
// does nothing there even if a stale click arrives.
func TestIPodTabHasNoClear(t *testing.T) {
	u := devLogUI(&fakeBackend{})
	openIPodTab(u)
	n := len(u.st.Log)
	u.clearLogBtn.Click()
	frame(u)
	if len(u.st.Log) != n || n == 0 {
		t.Errorf("Clear on the iPod tab emptied the window's log (%d → %d)", n, len(u.st.Log))
	}
}

// The real backend: a volume with no log is fs.ErrNotExist (the pane's "No
// log on the iPod yet"), not a failure.
func TestRealReadDeviceLogMissing(t *testing.T) {
	_, err := (&RealBackend{}).ReadDeviceLog(context.Background(), t.TempDir())
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v", err)
	}
}

// `--dialog ipodlog` is the drawer on The iPod with its canned two boots,
// and drawing it reads nothing: a screenshot has no iPod behind it.
func TestCannedIPodLog(t *testing.T) {
	f := &fakeBackend{}
	u := devLogUI(f)
	u.SetState(DemoState())
	if err := cannedDialog(u, "ipodlog"); err != nil {
		t.Fatal(err)
	}
	frame(u)
	if !u.st.LogOpen || u.st.LogTab != LogTabIPod || u.logFrames == 0 {
		t.Fatalf("drawer open %v on %v, drawn %d", u.st.LogOpen, u.st.LogTab, u.logFrames)
	}
	if len(f.devLogCalls) != 0 {
		t.Errorf("the canned drawer read the volume: %q", f.devLogCalls)
	}
	got := u.shownLogText()
	if !strings.HasPrefix(got, "=== Boot 12 · Core v0.2.2 (v0.2.2-5-g39fdaf9) · started Nov 2 at 14:03 ===\n") ||
		!strings.Contains(got, "=== Boot 11 · Core v0.2.1 · started Nov 1 at 12:03 · its first lines were overwritten ===\n"+
			"… 2 blocks missing here") {
		t.Errorf("canned iPod log:\n%s", got)
	}
}
