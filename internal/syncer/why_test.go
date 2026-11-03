package syncer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEveryOpSaysWhy: each decision MakePlan makes carries its CopyWhy
// beside its Reason — a fresh device is all new, an untouched one all same,
// and each way a file can differ has its own value.
func TestEveryOpSaysWhy(t *testing.T) {
	src, dst := srcTree(t), t.TempDir()
	o := opts(src, dst)

	p := planFor(t, o)
	if len(p.Copy) == 0 {
		t.Fatal("a fresh device copies nothing")
	}
	for _, op := range p.Copy {
		if op.Why != WhyNew || !op.Why.Copies() {
			t.Errorf("fresh device: %s is %q (%s), want new", op.Device, op.Why, op.Reason)
		}
	}
	syncOnce(t, o)
	p = planFor(t, o)
	for _, op := range p.Skip {
		if op.Why != WhySame || op.Why.Copies() {
			t.Errorf("second run: %s is %q (%s), want same", op.Device, op.Why, op.Reason)
		}
	}

	dir := filepath.Join(dst, "Music", "Artist A - Blue")
	alpha := filepath.Join(dir, "01. Alpha.flac")
	beta := filepath.Join(src, "Blue - Artist A", "Beta.flac")
	future := time.Now().Add(3 * time.Hour)
	if err := os.Chtimes(beta, future, future); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(alpha)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alpha, append(b, 0), 0o644); err != nil {
		t.Fatal(err)
	}
	p = planFor(t, o)
	got := map[string]CopyWhy{}
	for _, op := range p.Copy {
		got[filepath.Base(op.Dst)] = op.Why
	}
	if got["01. Alpha.flac"] != WhySize || got["02. Beta.flac"] != WhyTime || len(got) != 2 {
		t.Errorf("copies = %v, want Alpha size and Beta time", got)
	}

	// --verify: same size, same time, different bytes.
	syncOnce(t, o)
	fi, err := os.Stat(alpha)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(alpha)
	b[len(b)-1] ^= 0xFF
	if err := os.WriteFile(alpha, b, 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(alpha, fi.ModTime(), fi.ModTime())
	vo := o
	vo.Verify = true
	p = planFor(t, vo)
	if len(p.Copy) != 1 || p.Copy[0].Why != WhyContent {
		t.Errorf("--verify copies %+v, want Alpha for its content", p.Copy)
	}
	for _, op := range p.Skip {
		if op.Why != WhySame {
			t.Errorf("--verify skip %s is %q", op.Device, op.Why)
		}
	}

	// A case-only rename moves the file the device already has: same.
	syncOnce(t, o)
	if err := os.Rename(alpha, filepath.Join(dir, "01. ALPHA.flac")); err != nil {
		t.Fatal(err)
	}
	p = planFor(t, o)
	if len(p.Rename) != 1 || p.Rename[0].Why != WhySame {
		t.Errorf("rename = %+v, want one, same", p.Rename)
	}
}

func TestWhyCopies(t *testing.T) {
	for w, want := range map[CopyWhy]bool{
		WhyNew: true, WhySize: true, WhyTime: true, WhyContent: true, WhyUnreadable: true,
		WhySame: false, WhyHourShift: false, "": false,
	} {
		if w.Copies() != want {
			t.Errorf("%q.Copies() = %v", w, !want)
		}
	}
}

// The CLI's plan ends with the dry-run line; the window's has neither it
// nor a command-line flag in it.
func TestPlanTextForTheCLIAndTheWindow(t *testing.T) {
	p := &Plan{Src: "/src", Dst: "/dst", Prune: []string{"/dst/Music/Old - Gone"},
		Copy: []FileOp{{Device: "Music/A/01.flac", Size: 1, Reason: "not on the device", Why: WhyNew}}}
	var b bytes.Buffer
	WritePlan(&b, p, false)
	cli := b.String()
	if !strings.HasSuffix(cli, "\n"+DryRunFooter+"\n") || !strings.Contains(cli, "--prune --yes") {
		t.Errorf("CLI plan:\n%s", cli)
	}
	win := WindowPlan(p)
	for _, bad := range []string{"dry run", "Dry run", "prune", "--yes"} {
		if strings.Contains(win, bad) {
			t.Errorf("window plan contains %q:\n%s", bad, win)
		}
	}
	if !strings.Contains(win, "copy   Music/A/01.flac") || !strings.Contains(win, "Music/Old - Gone") {
		t.Errorf("window plan lost lines:\n%s", win)
	}
}
