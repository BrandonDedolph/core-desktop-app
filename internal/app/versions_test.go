package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BrandonDedolph/core-desktop-app/internal/version"
)

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.1.9", "v0.2.0", true},
		{"v0.2.0", "v0.2.0", false},
		{"v0.10.0", "v0.2.0", false}, // numeric
		{"v0.2.1", "v0.2.0", false},
		{"v1.0.0", "v0.9.9", false},
		{"weird", "v0.2.0", false},
	}
	for _, c := range cases {
		if got := versionLess(c.a, c.b); got != c.want {
			t.Errorf("versionLess(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// An iPod on firmware older than MinFirmware: the header and the Firmware
// row say so, Sync is not the primary (Update is, when a release is known),
// and a real sync refuses while the preview still runs.
func TestFirmwareOlderThanTheAppWrites(t *testing.T) {
	u := NewUI(Options{})
	st := DemoState()
	st.Device.Firmware = "v0.1.9 (build x)"
	u.SetState(st)
	if !u.st.FirmwareTooOld() {
		t.Fatal("v0.1.9 is not too old")
	}
	if s, col := u.statusLine(); s != TooOldText || col != u.pal.Accent {
		t.Errorf("line 2 = %q", s)
	}
	if got := u.st.PrimaryLabel(); got != "Update to v0.2.2" {
		t.Errorf("primary = %q", got)
	}
	if fw := rowByLabel(t, u, "Firmware"); fw.sub != TooOldText || len(fw.actions) != 1 || fw.actions[0].label != "Update" {
		t.Errorf("Firmware row = %+v", fw)
	}
	u.st.Release = Release{}
	if got := u.st.Primary(); got != ActionEject {
		t.Errorf("no release known: primary = %v", got)
	}
	// The boundary: exactly MinFirmware is fine.
	u.st.Device.Firmware = MinFirmware + " (build y)"
	if u.st.FirmwareTooOld() {
		t.Error("MinFirmware itself counted as too old")
	}
	// No marker at all is not "too old".
	u.st.Device.Firmware = ""
	if u.st.FirmwareTooOld() {
		t.Error("an unknown version counted as too old")
	}
}

func TestSyncRefusesOldFirmwareButPreviewRuns(t *testing.T) {
	f := &fakeBackend{}
	u := newTestUI(f)
	u.st.Device.Firmware = "v0.1.9"
	u.startSync(JobSync)
	settle(u)
	if len(f.syncCalls) != 0 || u.st.Job == nil || !u.st.Job.Failed || u.st.Job.Text != TooOldText {
		t.Errorf("sync on old firmware: calls %d, job %+v", len(f.syncCalls), u.st.Job)
	}
	u.startSync(JobDryRun)
	settle(u)
	if len(f.syncCalls) != 1 || !f.syncCalls[0].DryRun {
		t.Errorf("the preview did not run: %+v", f.syncCalls)
	}
}

func withVersion(t *testing.T, v string) {
	t.Helper()
	old := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = old })
}

// The launch check also asks about the app, once: a newer app release
// reaches About and the menu, and nothing is logged.
func TestLaunchCheckFindsANewerApp(t *testing.T) {
	withVersion(t, "v0.3.0")
	f := &fakeBackend{
		dev:    Device{Found: true, Path: `\\.\PhysicalDrive2`, Firmware: "v0.2.1"},
		rel:    Release{Checked: true, Tag: "v0.2.1", Asset: "core.ipod"},
		appTag: "v0.3.1", appURL: "https://github.com/x/y/releases/tag/v0.3.1",
	}
	u := newTestUI(f)
	u.st.Source = ""
	u.startRefresh()
	settleWindow(u)
	if f.appCalls != 1 {
		t.Fatalf("app checks = %d", f.appCalls)
	}
	if u.st.NewerApp != "v0.3.1" || !u.menuVisible(menuGetApp) {
		t.Errorf("newer app = %q, menu item visible %v", u.st.NewerApp, u.menuVisible(menuGetApp))
	}
	about := strings.Join(u.aboutLines("core.exe"), "\n")
	if !strings.Contains(about, "Core app v0.3.1 is available: ··· → Update the app to v0.3.1 downloads it and restarts.") ||
		!strings.Contains(about, "The release: https://github.com/x/y/releases/tag/v0.3.1") ||
		!strings.Contains(about, "writes files for Core v0.2.0 or newer") {
		t.Errorf("About:\n%s", about)
	}
	// The firmware is up to date: the app release is not a firmware one.
	if u.st.UpdateAvailable() {
		t.Error("the app release made the firmware look out of date")
	}
	if strings.Contains(strings.Join(u.st.Log, "\n"), "app") && strings.Contains(strings.Join(u.st.Log, "\n"), "could not") {
		t.Errorf("a successful app check logged: %q", u.st.Log)
	}
	// The menu's Check for updates is the firmware only.
	u.startCheck()
	settleWindow(u)
	if f.appCalls != 1 {
		t.Errorf("the menu check asked about the app too: %d", f.appCalls)
	}
}

// A developer build (no release tag) is never nagged; a failed app check is one
// log line and leaves the firmware check alone.
func TestAppCheckQuietCases(t *testing.T) {
	withVersion(t, "1a2b3c4-dirty")
	f := &fakeBackend{
		dev:    Device{Found: true, Path: `\\.\PhysicalDrive2`},
		rel:    Release{Checked: true, Tag: "v0.2.1", Asset: "core.ipod"},
		appTag: "v9.9.9", appURL: "u",
	}
	u := newTestUI(f)
	u.st.Source = ""
	u.startRefresh()
	settleWindow(u)
	if u.st.NewerApp != "" || u.menuVisible(menuGetApp) {
		t.Errorf("a dev build was told about %q", u.st.NewerApp)
	}

	withVersion(t, "v0.3.0")
	f2 := &fakeBackend{
		dev:    Device{Found: true, Path: `\\.\PhysicalDrive2`},
		rel:    Release{Checked: true, Tag: "v0.2.1", Asset: "core.ipod"},
		appErr: errors.New("dial tcp: no route"),
	}
	u2 := newTestUI(f2)
	u2.st.Source = ""
	u2.startRefresh()
	settleWindow(u2)
	if u2.st.Release.Tag != "v0.2.1" || u2.st.Job.Failed {
		t.Errorf("the firmware check suffered: %+v %+v", u2.st.Release, u2.st.Job)
	}
	if !strings.Contains(strings.Join(u2.st.Log, "\n"), "could not check for a newer app: dial tcp") {
		t.Errorf("no log line for the failed app check: %q", u2.st.Log)
	}
}

// takeDialog waits for the job's question and returns it unanswered.
func takeDialog(t *testing.T, u *UI) *dialogRequest {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		u.mu.Lock()
		req := u.askQueue
		u.askQueue = nil
		u.mu.Unlock()
		if req != nil {
			return req
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no dialog")
	return nil
}

// newerAppUI is a window whose launch check found v0.3.1.
func newerAppUI(f *fakeBackend) *UI {
	u := newTestUI(f)
	u.st.NewerApp, u.st.NewerAppURL = "v0.3.1", "https://example/r"
	return u
}

// The menu item names the release, and Cancel on its one question runs
// nothing: no download, no swap, no restart.
func TestUpdateTheAppCancelRunsNothing(t *testing.T) {
	f := &fakeBackend{}
	u := newerAppUI(f)
	u.SetState(MenuState())
	u.st.NewerApp, u.st.NewerAppURL = "v0.3.1", "https://example/r"
	if !u.menuVisible(menuGetApp) || !u.menuEnabled(menuGetApp) {
		t.Fatal("the menu item is not offered")
	}
	u.Layout(newTestContext(900, 600))
	u.menuClicks[menuGetApp].Click()
	u.Layout(newTestContext(900, 600))

	req := takeDialog(t, u)
	if req.title != "Update the app?" ||
		req.body != "It downloads v0.3.1, replaces this program and core.exe beside it, and restarts." ||
		req.okLabel != "Yes" || req.want != "" || req.solo() {
		t.Errorf("dialog = %q / %q / %q (typed %q, solo %v)", req.title, req.body, req.okLabel, req.want, req.solo())
	}
	req.reply <- CancelAnswer
	settle(u)
	if len(f.appUpdates) != 0 || len(f.relaunched) != 0 {
		t.Errorf("Cancel ran: updates %v, relaunched %v", f.appUpdates, f.relaunched)
	}
	if u.st.Job == nil || u.st.Job.Failed {
		t.Errorf("job = %+v", u.st.Job)
	}
}

// Yes downloads (the header shows the download's percentage), swaps and
// restarts the program UpdateApp names.
func TestUpdateTheAppYesUpdatesAndRestarts(t *testing.T) {
	f := &fakeBackend{}
	u := newerAppUI(f)
	u.startAppUpdate()
	req := takeDialog(t, u)
	req.reply <- "yes"
	settle(u)
	if len(f.appUpdates) != 1 || f.appUpdates[0] != "v0.3.1" {
		t.Errorf("updates = %v", f.appUpdates)
	}
	if len(f.relaunched) != 1 || f.relaunched[0] != `C:\Core\core-app.exe` {
		t.Errorf("relaunched = %v", f.relaunched)
	}
	log := strings.Join(u.st.Log, "\n")
	if !strings.Contains(log, "--- app update ---") || !strings.Contains(log, "updated to v0.3.1; starting it") {
		t.Errorf("log:\n%s", log)
	}
}

// The header carries the download's progress while it runs.
func TestUpdateTheAppProgressInTheHeader(t *testing.T) {
	u := NewUI(Options{})
	u.st.Job = &JobStatus{Kind: JobAppUpdate, Pct: -1}
	u.st.Apply(Event{Kind: EventProgress, Job: JobAppUpdate, Text: "downloading core-app-windows-amd64.exe", Pct: 0.42})
	if s, _ := u.statusLine(); s != "app update: downloading core-app-windows-amd64.exe  ·  42%" {
		t.Errorf("header = %q", s)
	}
}

// A failure restarts nothing, and the log says why and that nothing changed.
func TestUpdateTheAppFailureRestartsNothing(t *testing.T) {
	f := &fakeBackend{appUpdateErr: errors.New("ghrelease: the downloaded image does not verify: core.exe")}
	u := newerAppUI(f)
	u.startAppUpdate()
	takeDialog(t, u).reply <- "yes"
	settle(u)
	if len(f.relaunched) != 0 {
		t.Errorf("relaunched after a failure: %v", f.relaunched)
	}
	if u.st.Job == nil || !u.st.Job.Failed {
		t.Fatalf("job = %+v", u.st.Job)
	}
	log := strings.Join(u.st.Log, "\n")
	if !strings.Contains(log, "does not verify: core.exe — the app was left as it was") {
		t.Errorf("log:\n%s", log)
	}
}

// The menu item is greyed while another job runs: an update must not
// start under a sync.
func TestUpdateTheAppWaitsForOtherJobs(t *testing.T) {
	u := newerAppUI(&fakeBackend{})
	u.st.Job = &JobStatus{Kind: JobSync, Pct: -1}
	if u.menuEnabled(menuGetApp) {
		t.Error("Update the app is enabled during a sync")
	}
}
