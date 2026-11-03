// Package selfupdate replaces this program, and the other one beside it,
// with an app release's binaries: pick this platform's assets, download
// them through ghrelease into the cache, verify each against the
// `<asset>.sha256` the release publishes beside it, then swap them in.
//
// The swap is the part with an operating system in it. On Windows a
// running executable cannot be overwritten or deleted, but it can be
// renamed: core-app.exe becomes core-app.old.exe, the new file takes its
// name, and the next start (CleanOld) deletes the .old one. On Linux and
// macOS a rename over the running file is allowed — the process keeps the
// inode it was started from — so the new file is written beside the old
// one, given its mode bits, and renamed over it.
//
// What this package will not do: run without a checksum (a release with
// no `.sha256` for a binary is refused, not trusted), write into a folder
// this user cannot write (Program Files without elevation is told where
// the release is instead), or leave a half-swapped pair: both new files
// are staged in the program's folder before either old one is touched,
// and a failure part-way through the Windows renames puts the old names
// back. The checksum is an integrity check, like the `.ipod` one — this
// project has no release key.
package selfupdate

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BrandonDedolph/core-desktop-app/internal/ghrelease"
)

// The two programs a release carries, by the stem of their asset names.
const (
	App = "core-app"
	CLI = "core"
)

// ChecksumSuffix is what the release workflow appends to a binary's name
// for the file holding its SHA-256, in sha256sum's format.
const ChecksumSuffix = ".sha256"

// Errors callers distinguish.
var (
	// ErrNoAsset: the release has no binary for this platform.
	ErrNoAsset = errors.New("selfupdate: the release has no binary for this computer")
	// ErrNoChecksum: the binary is there and its checksum file is not.
	// An unverifiable program is not installed.
	ErrNoChecksum = errors.New("selfupdate: the release publishes no checksum for the binary")
	// ErrNotWritable: the program's folder cannot be written by this
	// user, so nothing in it can be replaced.
	ErrNotWritable = errors.New("selfupdate: the program's folder cannot be written")
)

// AssetName is the release's name for prog's binary on goos/goarch:
// "core-app-windows-amd64.exe", "core-linux-arm64".
func AssetName(prog, goos, goarch string) string {
	n := prog + "-" + goos + "-" + goarch
	if goos == "windows" {
		n += ".exe"
	}
	return n
}

// Pick is one binary off a release and the checksum file beside it.
type Pick struct {
	Bin, Sum ghrelease.Asset
}

// PickAsset finds prog's binary for goos/goarch on rel, and its checksum.
func PickAsset(rel ghrelease.Release, prog, goos, goarch string) (Pick, error) {
	name := AssetName(prog, goos, goarch)
	var p Pick
	var haveBin, haveSum bool
	for _, a := range rel.Assets {
		switch {
		case strings.EqualFold(a.Name, name):
			p.Bin, haveBin = a, true
		case strings.EqualFold(a.Name, name+ChecksumSuffix):
			p.Sum, haveSum = a, true
		}
	}
	if !haveBin {
		return Pick{}, fmt.Errorf("%w: %s carries no %s", ErrNoAsset, rel.Tag, name)
	}
	if !haveSum {
		return Pick{}, fmt.Errorf("%w: %s carries %s but no %s, so it cannot be verified",
			ErrNoChecksum, rel.Tag, name, name+ChecksumSuffix)
	}
	return p, nil
}

// ParseSum reads the SHA-256 for name out of a checksum file: sha256sum's
// "<hex>  <name>" (or "<hex> *<name>"), one line or many, or a bare hex
// digest on its own. A line that names a different file does not count.
func ParseSum(text, name string) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 || !isHex64(f[0]) {
			continue
		}
		if len(f) == 1 || strings.EqualFold(strings.TrimPrefix(f[1], "*"), name) {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("%w: no SHA-256 for %s in its checksum file", ErrNoChecksum, name)
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

// Target is one installed program to replace: which release program it
// is and the file on disk.
type Target struct {
	Prog string
	Path string
}

// Fetched is a target with its new binary downloaded and verified.
type Fetched struct {
	Target
	New string
}

// Updater is the download half. The zero value works for this computer.
type Updater struct {
	// Client is the release client; nil means ghrelease.New().
	Client *ghrelease.Client
	// GOOS and GOARCH pick the assets; "" means this build's.
	GOOS, GOARCH string
	// CachePath is where a download is kept; nil means ghrelease.CachePath.
	CachePath func(tag, name string) (string, error)
	// Progress is told how a binary's download is going.
	Progress func(name string, done, total int64)
	// Log is told each step in a sentence.
	Log func(string)
}

func (u *Updater) goos() string {
	if u.GOOS != "" {
		return u.GOOS
	}
	return runtime.GOOS
}

func (u *Updater) goarch() string {
	if u.GOARCH != "" {
		return u.GOARCH
	}
	return runtime.GOARCH
}

func (u *Updater) log(format string, args ...any) {
	if u.Log != nil {
		u.Log(fmt.Sprintf(format, args...))
	}
}

func (u *Updater) client() *ghrelease.Client {
	if u.Client != nil {
		return u.Client
	}
	return ghrelease.New()
}

func (u *Updater) cachePath(tag, name string) (string, error) {
	if u.CachePath != nil {
		return u.CachePath(tag, name)
	}
	return ghrelease.CachePath(tag, name)
}

// Fetch checks every target's folder can be written, then downloads and
// verifies each target's binary. Nothing is replaced here: a failure
// leaves the installed programs exactly as they were, and a download that
// does not verify is deleted.
func (u *Updater) Fetch(ctx context.Context, rel ghrelease.Release, targets []Target) ([]Fetched, error) {
	for _, t := range targets {
		if err := CheckWritable(filepath.Dir(t.Path)); err != nil {
			return nil, err
		}
	}
	picks := make([]Pick, len(targets))
	for i, t := range targets {
		p, err := PickAsset(rel, t.Prog, u.goos(), u.goarch())
		if err != nil {
			return nil, err
		}
		picks[i] = p
	}
	var out []Fetched
	for i, t := range targets {
		path, err := u.fetchOne(ctx, rel.Tag, picks[i])
		if err != nil {
			return nil, err
		}
		out = append(out, Fetched{Target: t, New: path})
	}
	return out, nil
}

func (u *Updater) fetchOne(ctx context.Context, tag string, p Pick) (string, error) {
	c := *u.client()
	c.Progress = nil
	// The checksum file is fetched every time, never trusted from the
	// cache: it is what the binary in the cache is checked against.
	sumPath, err := u.cachePath(tag, p.Sum.Name)
	if err != nil {
		return "", err
	}
	os.Remove(sumPath)
	if err := c.Download(ctx, p.Sum, sumPath); err != nil {
		return "", fmt.Errorf("downloading %s: %w", p.Sum.Name, err)
	}
	text, err := os.ReadFile(sumPath)
	if err != nil {
		return "", fmt.Errorf("selfupdate: %w", err)
	}
	sum, err := ParseSum(string(text), p.Bin.Name)
	if err != nil {
		return "", err
	}

	dst, err := u.cachePath(tag, p.Bin.Name)
	if err != nil {
		return "", err
	}
	if u.Progress != nil {
		name := p.Bin.Name
		c.Progress = func(done, total int64) { u.Progress(name, done, total) }
	}
	u.log("downloading %s (%d bytes)", p.Bin.Name, p.Bin.Size)
	if err := c.DownloadSHA256(ctx, p.Bin, dst, sum); err != nil {
		// A cached copy that failed is not left to be found again.
		if errors.Is(err, ghrelease.ErrChecksum) {
			os.Remove(dst)
		}
		return "", err
	}
	u.log("verified: %s (SHA-256 %s)", dst, sum[:12]+"…")
	return dst, nil
}

// CheckWritable proves dir can take a new file by making one and removing
// it. The error names the folder.
func CheckWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".core-write-test-*")
	if err != nil {
		return fmt.Errorf("%w: %s (%v); a folder like Program Files needs an administrator, "+
			"so download the release yourself and replace the files there", ErrNotWritable, dir, err)
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return nil
}

// OldName is what a replaced program is renamed to on Windows:
// core-app.exe → core-app.old.exe.
func OldName(path string) string {
	ext := filepath.Ext(path)
	return strings.TrimSuffix(path, ext) + ".old" + ext
}

// Swap replaces the program at exePath with the file at newPath, the
// way this operating system allows. See SwapAll.
func Swap(exePath, newPath string) error {
	return SwapAll(runtime.GOOS, []Fetched{{Target: Target{Path: exePath}, New: newPath}})
}

// SwapAll replaces each target with its new file. Every new file is first
// copied into its target's folder (a rename cannot cross drives, and the
// cache is usually on another) with the target's mode bits; only when all
// are staged is any target touched. On windows each target is renamed to
// OldName and the staged file renamed into its place, and a failure puts
// the old names back; elsewhere the staged file is renamed over the
// target. newPath files are left where they are (they are the cache).
func SwapAll(goos string, fs []Fetched) error {
	staged := make([]string, len(fs))
	clean := func() {
		for _, s := range staged {
			if s != "" {
				os.Remove(s)
			}
		}
	}
	for i, f := range fs {
		s, err := stage(f.Path, f.New)
		if err != nil {
			clean()
			return err
		}
		staged[i] = s
	}

	if goos != "windows" {
		for i, f := range fs {
			if err := os.Rename(staged[i], f.Path); err != nil {
				clean()
				return fmt.Errorf("selfupdate: replacing %s: %w", f.Path, err)
			}
			staged[i] = ""
		}
		return nil
	}

	var done []int
	undo := func() {
		for j := len(done) - 1; j >= 0; j-- {
			p := fs[done[j]].Path
			os.Rename(OldName(p), p)
		}
		clean()
	}
	for i, f := range fs {
		old := OldName(f.Path)
		// A .old from an update whose cleanup never ran: it is not
		// running (this program is the one at f.Path), so it can go.
		if err := os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
			undo()
			return fmt.Errorf("selfupdate: removing the earlier %s: %w", old, err)
		}
		if err := os.Rename(f.Path, old); err != nil {
			undo()
			return fmt.Errorf("selfupdate: renaming %s out of the way: %w", f.Path, err)
		}
		done = append(done, i)
		if err := os.Rename(staged[i], f.Path); err != nil {
			undo()
			return fmt.Errorf("selfupdate: moving the new %s into place: %w", filepath.Base(f.Path), err)
		}
		staged[i] = ""
	}
	return nil
}

// stage copies src beside target under a hidden temporary name, with
// target's mode bits, synced, and returns its path.
func stage(target, src string) (string, error) {
	st, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("selfupdate: %w", err)
	}
	in, err := os.Open(src)
	if err != nil {
		return "", fmt.Errorf("selfupdate: %w", err)
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".new-*")
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrNotWritable, filepath.Dir(target), err)
	}
	name := out.Name()
	fail := func(err error) (string, error) {
		out.Close()
		os.Remove(name)
		return "", fmt.Errorf("selfupdate: staging %s: %w", filepath.Base(target), err)
	}
	if _, err := io.Copy(out, in); err != nil {
		return fail(err)
	}
	if err := out.Chmod(st.Mode().Perm()); err != nil {
		return fail(err)
	}
	if err := out.Sync(); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		os.Remove(name)
		return "", fmt.Errorf("selfupdate: staging %s: %w", filepath.Base(target), err)
	}
	return name, nil
}

// CleanOld deletes what an earlier update left in dir: the renamed-away
// programs (core*.old.exe, and core*.old elsewhere) and any staged file a
// failed swap could not remove. Only names this package makes are
// touched; a file still in use stays for the next start. It returns what
// it deleted.
func CleanOld(dir string) []string {
	var gone []string
	for _, pat := range []string{"core*.old.exe", "core*.old", ".core*.new-*"} {
		m, _ := filepath.Glob(filepath.Join(dir, pat))
		for _, p := range m {
			if os.Remove(p) == nil {
				gone = append(gone, p)
			}
		}
	}
	return gone
}

// Sibling is the other program of the pair beside exe, if it is there:
// for the app, core(.exe); for the CLI, core-app(.exe).
func Sibling(goos, exe, prog string) (string, bool) {
	name := prog
	if goos == "windows" {
		name += ".exe"
	}
	p := filepath.Join(filepath.Dir(exe), name)
	if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
		return p, true
	}
	return "", false
}

// Executable is this program's own file, symlinks resolved. Read it
// BEFORE a swap: on Linux os.Executable is /proc/self/exe, which names
// the replaced file " (deleted)" once the new one is in.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe, nil
}

// Seams for Relaunch's test.
var (
	startProcess = func(c *exec.Cmd) error { return c.Start() }
	exit         = os.Exit
)

// Relaunch starts exe with args, this process's standard streams and
// working directory, and exits this process. It returns only when the new
// process could not be started.
func Relaunch(exe string, args []string) error {
	c := exec.Command(exe, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := startProcess(c); err != nil {
		return fmt.Errorf("selfupdate: starting %s: %w", exe, err)
	}
	exit(0)
	return nil
}
