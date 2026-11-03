package app

import (
	"context"
	"errors"
	"image"
	"sync"
	"time"

	"github.com/BrandonDedolph/core-desktop-app/internal/artfetch"
	"github.com/BrandonDedolph/core-desktop-app/internal/disk"
	"github.com/BrandonDedolph/core-desktop-app/internal/doctor"
	"github.com/BrandonDedolph/core-desktop-app/internal/flasher"
	"github.com/BrandonDedolph/core-desktop-app/internal/fwpart"
	"github.com/BrandonDedolph/core-desktop-app/internal/installer"
	"github.com/BrandonDedolph/core-desktop-app/internal/librarian"
	"github.com/BrandonDedolph/core-desktop-app/internal/organizer"
	"github.com/BrandonDedolph/core-desktop-app/internal/syncer"
)

// fakeBackend is the Backend every test in this package runs against.
//
// It exists so the answer to "does the Sync + prune button really do a
// dry run first, and really refuse to delete until the second dialog is
// answered with the exact word" is a test and not a paragraph. Nothing
// in here touches a disk, a network or a window.
type fakeBackend struct {
	mu sync.Mutex

	dev Device
	lib Library
	rel Release

	// plans is handed out one per Sync call, so a prune test can make
	// the dry run report orphans.
	plans []*syncer.Plan

	// block, when set, makes Sync wait for it (or for the context) so
	// the busy/cancel tests have something to race against.
	block chan struct{}

	refreshErr, syncErr, releaseErr, downloadErr, flashErr, backupErr, ejectErr error

	// The detect script: one []disk.IPod per Detect call, the last one
	// repeating forever. Empty means "nothing is scripted" and Refresh
	// answers with dev/lib as it always did.
	pods        [][]disk.IPod
	detectCalls int
	detectErr   error
	current     []disk.IPod
	// installed is what Classify says about each device path. The fake
	// installer rewrites it, which is how a test sees the phase move
	// from NotInstalled to Ready for the reason a real one would.
	installed   map[string]fwpart.Installed
	classifyErr error

	// volumeDir stands in for the mounted FAT volume during an install.
	volumeDir    string
	installCalls []installer.Options
	installFlash flasher.Options
	installAns   string
	installErr   error

	// releaseCalls and refreshCalls count the two reads the header's
	// after-job behaviour adds; flashFirmware is what a successful fake
	// Flash leaves installed, so a test can see the header follow it.
	appTag, appURL string
	appErr         error
	appCalls       int
	// appUpdates are the tags UpdateApp was asked for; appUpdateErr is
	// its answer; relaunched is what RelaunchApp was handed.
	appUpdates   []string
	appUpdateErr error
	relaunched   []string

	backupsApple time.Time
	backupsN     int
	backupsDir   string

	releaseCalls  int
	refreshCalls  int
	flashFirmware string

	// devLog/devLogErr are what ReadDeviceLog answers; devLogCalls
	// counts the reads and their volumes, and readInSync records a read
	// that came while a Sync was still running — which must never happen.
	devLog      []doctor.BootLog
	devLogErr   error
	devLogCalls []string
	inSync      bool
	readInSync  bool

	syncCalls   []syncer.Options
	flashCalls  []string
	flashPrompt string
	flashAnswer string
	backupCalls int
	ejectCalls  []string
	renameCalls []renameCall
	renameErr   error
	downloaded  string

	// The library manager's half: what Inspect answers, what the
	// lookup found, and a record of every Fix and Undo so a test can
	// prove the button ran once and with the choices on screen.
	report       *librarian.Report
	inspectCalls []string
	inspectDiscs []bool
	inspectErr   error
	cands        map[librarian.AlbumKey][]artfetch.Candidate
	candCalls    int
	candErr      error
	fixCalls     []librarian.Choices
	fixResult    *librarian.Result
	fixErr       error
	undoCalls    []string
	undoReport   *organizer.UndoReport
	undoErr      error

	// thumbs answers Thumbnail per album folder; thumbCalls and
	// fetchCalls are what the cache actually asked for.
	thumbs     map[string]image.Image
	thumbCalls []string
	thumbErr   error
	fetchCalls []string

	// keepCalls is every RemoveDuplicate: {keep, drop}.
	keepCalls [][2]librarian.AlbumKey
	// applyCalls is every ApplyCover; coverFail makes it fail.
	applyCalls []applyCall
	coverFail  string

	// editCalls is every EditTags; editResult and editErr are its answer.
	editCalls  []editCall
	editResult *librarian.EditResult
	editErr    error
}

type editCall struct {
	root, dir, after string
	edits            []librarian.TagEdit
}

// EditTags records the Save and answers with editResult (by default: every
// edit written, nothing renamed, a journal).
func (f *fakeBackend) EditTags(ctx context.Context, root, dir string, edits []librarian.TagEdit, after string,
	emit func(Event)) (*librarian.EditResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.editCalls = append(f.editCalls, editCall{root, dir, after, edits})
	if f.editErr != nil {
		return nil, f.editErr
	}
	if f.editResult != nil {
		return f.editResult, nil
	}
	return &librarian.EditResult{Journal: "/journal/edit-fix.json", Written: len(edits), Dir: dir}, nil
}

func (f *fakeBackend) Inspect(ctx context.Context, src string, discFolders bool,
	emit func(Event)) (*librarian.Report, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inspectCalls = append(f.inspectCalls, src)
	f.inspectDiscs = append(f.inspectDiscs, discFolders)
	if f.inspectErr != nil {
		return nil, f.inspectErr
	}
	emit(Event{Kind: EventLog, Text: "scanned " + src})
	return f.report, nil
}

func (f *fakeBackend) Candidates(ctx context.Context, rep *librarian.Report, emit func(Event)) (
	map[librarian.AlbumKey][]artfetch.Candidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.candCalls++
	return f.cands, f.candErr
}

func (f *fakeBackend) Fix(ctx context.Context, rep *librarian.Report, ch librarian.Choices,
	emit func(Event)) (*librarian.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fixCalls = append(f.fixCalls, ch)
	if f.fixErr != nil {
		return nil, f.fixErr
	}
	emit(Event{Kind: EventLog, Text: "fixing"})
	if f.fixResult == nil {
		return &librarian.Result{Journal: "/journal/one.json", Renamed: 1}, nil
	}
	return f.fixResult, nil
}

func (f *fakeBackend) UndoFix(ctx context.Context, journal string, emit func(Event)) (
	*organizer.UndoReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.undoCalls = append(f.undoCalls, journal)
	if f.undoErr != nil {
		return nil, f.undoErr
	}
	if f.undoReport == nil {
		return &organizer.UndoReport{Journal: journal, Undone: 1}, nil
	}
	return f.undoReport, nil
}

func (f *fakeBackend) Thumbnail(dir string, px int) (image.Image, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.thumbCalls = append(f.thumbCalls, dir)
	if f.thumbErr != nil {
		return nil, f.thumbErr
	}
	if img, ok := f.thumbs[dir]; ok {
		return img, nil
	}
	return nil, ErrNoThumb
}

func (f *fakeBackend) FetchThumb(ctx context.Context, url string) (image.Image, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchCalls = append(f.fetchCalls, url)
	return image.NewNRGBA(image.Rect(0, 0, 8, 8)), nil
}

func (f *fakeBackend) Refresh(ctx context.Context) (Device, Library, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshCalls++
	if f.pods == nil {
		return f.dev, f.lib, f.refreshErr
	}
	// Scripted: the doctor's answer follows whatever the last Detect
	// returned, the way the real one follows the real device.
	if len(f.current) == 0 {
		return Device{Err: "disk: no iPod found"}, Library{}, f.refreshErr
	}
	p := f.current[0]
	return Device{
		Found:     true,
		Path:      p.Disk.Path,
		Serial:    p.Disk.Serial,
		Model:     p.Model,
		Size:      p.Disk.SizeBytes,
		Tested:    p.Tested,
		Volume:    `D:\`,
		Installed: f.installed[p.Disk.Path],
	}, f.lib, f.refreshErr
}

// Detect hands out the next scripted set. The last one repeats, so a
// test that scripts "iPod, then nothing" gets an unplug that stays
// unplugged however many times the poll runs.
func (f *fakeBackend) Detect(ctx context.Context) ([]disk.IPod, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.detectCalls++
	if len(f.pods) == 0 {
		f.current = nil
		return nil, f.detectErr
	}
	i := f.detectCalls - 1
	if i >= len(f.pods) {
		i = len(f.pods) - 1
	}
	f.current = f.pods[i]
	return f.current, f.detectErr
}

func (f *fakeBackend) Classify(ctx context.Context, pod disk.IPod) (fwpart.Installed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.installed[pod.Disk.Path], f.classifyErr
}

// Install runs the REAL internal/installer over a stub device and a
// stub flasher.
//
// A fake that just returned a Result would prove the button starts a
// job and nothing else; this way the app's test also proves that the
// sequence the button runs asks for the typed confirmation, is told
// this write is an install, and creates the device files on the volume.
func (f *fakeBackend) Install(ctx context.Context, o installer.Options, emit func(Event),
	confirm func(prompt string) (string, error)) (*installer.Result, error) {
	f.mu.Lock()
	f.installCalls = append(f.installCalls, o)
	pod := disk.IPod{Disk: disk.Disk{Path: f.dev.Path, Serial: f.dev.Serial}, Tested: true}
	ierr := f.installErr
	f.mu.Unlock()
	if ierr != nil {
		return nil, ierr
	}

	return installer.Install(ctx, o, installer.Deps{
		Inspect: func(context.Context) (installer.Device, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return installer.Device{Pod: pod, Installed: f.installed[pod.Disk.Path]}, nil
		},
		Flash: func(_ context.Context, fo flasher.Options, fd flasher.Deps) (*flasher.Result, error) {
			f.mu.Lock()
			if f.installed == nil {
				f.installed = map[string]fwpart.Installed{}
			}
			f.installFlash = fo
			before := f.installed[pod.Disk.Path]
			f.mu.Unlock()
			answer, err := fd.Confirm(flasher.ConfirmPrompt(pod.Disk.Path))
			if err != nil {
				return nil, err
			}
			f.mu.Lock()
			f.installAns = answer
			f.mu.Unlock()
			if answer != pod.Disk.Path {
				return &flasher.Result{Aborted: true, Device: pod.Disk.Path}, nil
			}
			f.mu.Lock()
			// The write happened: the device is one of ours now.
			f.installed[pod.Disk.Path] = fwpart.Installed{
				Kind: fwpart.Core, Version: "v0.1.3", BuildID: "v0.1.3",
				Description: "Core v0.1.3 (build v0.1.3)",
			}
			f.dev.Installed = f.installed[pod.Disk.Path]
			f.mu.Unlock()
			return &flasher.Result{
				Verified: true, Device: pod.Disk.Path, Installed: before,
				BackupPath: `C:\Users\you\AppData\Roaming\core\backups\apple-` +
					pod.Disk.Serial + `-2026-09-15.bin`,
			}, nil
		},
		Volume:    func(disk.IPod) string { return f.volumeDir },
		Out:       emitWriter(emit),
		Confirm:   confirm,
		ChildArgs: func(image string) []string { return []string{"install", image} },
	})
}

func (f *fakeBackend) Sync(ctx context.Context, o syncer.Options, emit func(Event)) (*syncer.Plan, error) {
	f.mu.Lock()
	f.syncCalls = append(f.syncCalls, o)
	n := len(f.syncCalls)
	block, err := f.block, f.syncErr
	var plan *syncer.Plan
	if n-1 < len(f.plans) {
		plan = f.plans[n-1]
	} else {
		plan = &syncer.Plan{Albums: 1}
	}
	f.mu.Unlock()

	emit(Event{Kind: EventLog, Text: "sync call " + itoa(n)})
	emit(Event{Kind: EventProgress, Text: "working", Pct: 0.5})
	if block != nil {
		f.mu.Lock()
		f.inSync = true
		f.mu.Unlock()
		defer func() {
			f.mu.Lock()
			f.inSync = false
			f.mu.Unlock()
		}()
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return plan, err
}

// UpdateApp records the tag and answers appUpdateErr, with a progress
// line the way the real one reports a download.
func (f *fakeBackend) UpdateApp(ctx context.Context, tag string, emit func(Event)) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appUpdates = append(f.appUpdates, tag)
	emit(Event{Kind: EventProgress, Text: "downloading core-app-windows-amd64.exe", Pct: 0.42})
	if f.appUpdateErr != nil {
		return "", f.appUpdateErr
	}
	return `C:\Core\core-app.exe`, nil
}

// RelaunchApp records the program it would start.
func (f *fakeBackend) RelaunchApp(exe string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.relaunched = append(f.relaunched, exe)
	return nil
}

// ReadDeviceLog answers from devLog/devLogErr and counts the call.
func (f *fakeBackend) ReadDeviceLog(ctx context.Context, volume string) ([]doctor.BootLog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.devLogCalls = append(f.devLogCalls, volume)
	if f.inSync {
		f.readInSync = true
	}
	return f.devLog, f.devLogErr
}

// AppRelease answers from appTag/appURL/appErr.
func (f *fakeBackend) AppRelease(ctx context.Context) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appCalls++
	return f.appTag, f.appURL, f.appErr
}

func (f *fakeBackend) Release(ctx context.Context) (Release, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releaseCalls++
	return f.rel, f.releaseErr
}

func (f *fakeBackend) Download(ctx context.Context, rel Release, emit func(Event)) (string, error) {
	emit(Event{Kind: EventLog, Text: "downloading " + rel.Tag})
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.downloaded, f.downloadErr
}

func (f *fakeBackend) Flash(ctx context.Context, file string,
	confirm func(prompt string) (string, error), emit func(Event)) error {
	emit(Event{Kind: EventLog, Text: "plan:\n  device  " + f.dev.Path + "\n  image   " + file})
	f.mu.Lock()
	prompt := f.flashPrompt
	f.flashCalls = append(f.flashCalls, file)
	f.mu.Unlock()

	answer, err := confirm(prompt)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.flashAnswer = answer
	ferr := f.flashErr
	f.mu.Unlock()
	if ferr != nil {
		return ferr
	}
	if answer != f.dev.Path {
		return errors.New("aborted at the confirmation prompt")
	}
	emit(Event{Kind: EventLog, Text: "VERIFIED"})
	f.mu.Lock()
	if f.flashFirmware != "" {
		f.dev.Firmware = f.flashFirmware
	}
	f.mu.Unlock()
	return nil
}

func (f *fakeBackend) Backup(ctx context.Context, emit func(Event)) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.backupCalls++
	return "/tmp/fwpart-1-x.bin", f.backupErr
}

// renameCall is one Rename, recorded so a test can prove the Save
// button calls the backend exactly once and with what the user typed.
type renameCall struct{ volume, name string }

// Rename does what the real one does to the string — the label is
// LegalLabel of the name — and nothing at all to a disk.
func (f *fakeBackend) Rename(ctx context.Context, volume, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renameCalls = append(f.renameCalls, renameCall{volume: volume, name: name})
	if f.renameErr != nil {
		return "", f.renameErr
	}
	return disk.LegalLabel(name), nil
}

func (f *fakeBackend) Eject(ctx context.Context, volume string, emit func(Event)) error {
	emit(Event{Kind: EventLog, Text: volume + ": dismounted and ejected"})
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ejectCalls = append(f.ejectCalls, volume)
	return f.ejectErr
}

// --- test plumbing -----------------------------------------------------

// newTestUI builds a UI with no window, no config file and the fake
// backend, plus a device already "found" so the buttons' preconditions
// are met.
func newTestUI(f *fakeBackend) *UI {
	if f.installed == nil {
		f.installed = map[string]fwpart.Installed{}
	}
	u := NewUI(Options{Backend: f})
	u.st.Device = Device{Found: true, Path: `\\.\PhysicalDrive2`, Volume: `D:\`, Tested: true}
	u.st.Source = "/src"
	u.sourceEd.SetText("/src")
	u.st.CLIPath = "/opt/core"
	return u
}

// settle waits for the running job and folds everything it said into
// the model — the window does this over several frames; a test does it
// in one call.
func settle(u *UI) {
	u.run.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for {
		u.drain()
		u.run.Drain(&u.st)
		if !u.st.Busy() || time.Now().After(deadline) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// answerDialogs replies to each dialog the job raises, in order. It
// stands in for the human clicking the modal's buttons.
func answerDialogs(u *UI, answers ...string) chan int {
	done := make(chan int, 1)
	go func() {
		n := 0
		deadline := time.Now().Add(2 * time.Second)
		for n < len(answers) && time.Now().Before(deadline) {
			u.mu.Lock()
			req := u.askQueue
			if req != nil {
				u.askQueue = nil
			}
			u.mu.Unlock()
			if req == nil {
				time.Sleep(time.Millisecond)
				continue
			}
			req.reply <- answers[n]
			n++
		}
		done <- n
	}()
	return done
}

// Backups answers from three fields a test sets.
func (f *fakeBackend) Backups(ctx context.Context, serial string) (time.Time, int, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.backupsApple, f.backupsN, f.backupsDir
}

// RemoveDuplicate records the keep and answers with a journal.
func (f *fakeBackend) RemoveDuplicate(ctx context.Context, rep *librarian.Report, keep, drop librarian.AlbumKey,
	emit func(Event)) (*librarian.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keepCalls = append(f.keepCalls, [2]librarian.AlbumKey{keep, drop})
	return &librarian.Result{Journal: "/journal/keep-fix.json", Trashed: 1}, nil
}

// ApplyCover records the click and answers with a replacement, or with
// coverFail as the album's failure.
func (f *fakeBackend) ApplyCover(ctx context.Context, rep *librarian.Report, key librarian.AlbumKey,
	c artfetch.Candidate, after string, emit func(Event)) (*librarian.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applyCalls = append(f.applyCalls, applyCall{key, c, after})
	if f.coverFail != "" {
		return &librarian.Result{Failures: []librarian.Failure{{Album: key, Reason: f.coverFail}}}, nil
	}
	return &librarian.Result{Journal: "/journal/cover-" + string(rune(0x30+len(f.applyCalls))) + "-fix.json", ArtWritten: 1,
		Replaced: []librarian.Replacement{{Album: key, From: "500×500", To: "1200×1200"}}}, nil
}

type applyCall struct {
	key   librarian.AlbumKey
	c     artfetch.Candidate
	after string
}
