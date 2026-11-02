package flasher

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// The names BackupFileName makes, read back: Z, a positive offset and a
// negative one (whose colon became the second dash of the pair).
func TestBackupTimeReadsTheName(t *testing.T) {
	for _, tc := range []struct {
		name string
		want time.Time
		ok   bool
	}{
		{"fwpart-100-2026-01-02T03-04-05Z.bin", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), true},
		{"fwpart-100-2026-01-02T03-04-05+02-00.bin", time.Date(2026, 1, 2, 1, 4, 5, 0, time.UTC), true},
		{"fwpart-100-2026-01-02T03-04-05-05-30.bin", time.Date(2026, 1, 2, 8, 34, 5, 0, time.UTC), true},
		{"fwpart-100-whenever.bin", time.Time{}, false},
		{"fwpart-.bin", time.Time{}, false},
	} {
		got, ok := backupTime(tc.name)
		if ok != tc.ok || (ok && !got.Equal(tc.want)) {
			t.Errorf("%s: %v %v, want %v %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
	// And every name BackupFileName makes parses back to its time.
	at := time.Date(2026, 11, 2, 9, 8, 7, 0, time.FixedZone("x", -7*3600))
	if got, ok := backupTime(BackupFileName(131475456, at)); !ok || !got.Equal(at) {
		t.Errorf("round trip: %v %v", got, ok)
	}
}

// retainDir is a backup folder whose directory order, mtimes and embedded
// times all disagree, so a test can tell which one the retention read.
func retainDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := []struct {
		name  string
		mtime time.Time
	}{
		// Embedded order, newest first: b, c, a, d; "junk" has no time in
		// its name and the oldest mtime. The mtimes run the other way.
		{"fwpart-100-2026-01-01T00-00-00Z.bin", time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)},      // a
		{"fwpart-100-2026-03-01T00-00-00Z.bin", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},      // b
		{"fwpart-100-2026-02-01T00-00-00+02-00.bin", time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)}, // c
		{"fwpart-100-2025-12-01T00-00-00-05-00.bin", time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)}, // d
		{"fwpart-100-junk.bin", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"apple-SERIAL-2020-01-01.bin", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"notes.txt", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"fwpart-100-2019-01-01T00-00-00Z.txt", time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, f := range files {
		p := filepath.Join(dir, f.name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, f.mtime, f.mtime); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestBackupsToRemove(t *testing.T) {
	const (
		a    = "fwpart-100-2026-01-01T00-00-00Z.bin"
		b    = "fwpart-100-2026-03-01T00-00-00Z.bin"
		c    = "fwpart-100-2026-02-01T00-00-00+02-00.bin"
		d    = "fwpart-100-2025-12-01T00-00-00-05-00.bin"
		junk = "fwpart-100-junk.bin"
		// A backup not yet on disk: a dry run's.
		next = "fwpart-100-2026-04-01T00-00-00Z.bin"
	)
	for _, tc := range []struct {
		name   string
		n      int
		pinned []string
		remove []string
	}{
		{"0 keeps all", 0, nil, nil},
		{"newest 2 by the name", 2, nil, []string{a, d, junk}},
		{"more than there are", 10, nil, nil},
		{"the one being written takes a slot", 2, []string{next}, []string{c, a, d, junk}},
		// a is the oldest-but-one by its name and by no means the newest
		// mtime: pinned, it stays and is the one kept.
		{"the one just written is kept whatever its age", 1, []string{a}, []string{b, c, d, junk}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := retainDir(t)
			var pinned []string
			for _, p := range tc.pinned {
				pinned = append(pinned, filepath.Join(dir, p))
			}
			got := backupsToRemove(dir, tc.n, pinned)
			var names []string
			for _, p := range got {
				names = append(names, filepath.Base(p))
			}
			want := append([]string(nil), tc.remove...)
			sort.Strings(names)
			sort.Strings(want)
			if strings.Join(names, ",") != strings.Join(want, ",") {
				t.Errorf("remove %v, want %v", names, want)
			}
			for _, n := range names {
				if !strings.HasPrefix(n, "fwpart-") || !strings.HasSuffix(n, ".bin") {
					t.Errorf("%s is not a fwpart backup and was listed", n)
				}
			}
		})
	}
}

// Through a whole flash: keep_backups 1 leaves the backup this flash took
// and the Apple one, removes the older fwpart files and says so; the dry
// run says what would go and removes nothing.
func TestFlashKeepsTheNewestBackups(t *testing.T) {
	for _, dry := range []bool{true, false} {
		e := newEnv(t, syntheticPartition(t, plausibleImage(8192, 1), plausibleImage(4096, 2)))
		path := writeIPod(t, t.TempDir(), "core.ipod", plausibleImage(12345, 3))
		if err := os.MkdirAll(e.backupDir, 0o755); err != nil {
			t.Fatal(err)
		}
		old := []string{"fwpart-100-2020-01-01T00-00-00Z.bin", "fwpart-100-2021-01-01T00-00-00Z.bin",
			"apple-TESTSERIAL-2019-01-01.bin"}
		for _, n := range old {
			if err := os.WriteFile(filepath.Join(e.backupDir, n), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		_, err := Flash(context.Background(), Options{Image: path, Yes: true, BackupDir: e.backupDir,
			KeepBackups: 1, DryRun: dry}, e.deps())
		if err != nil {
			t.Fatalf("dry %v: Flash: %v", dry, err)
		}
		entries, _ := os.ReadDir(e.backupDir)
		var left []string
		for _, en := range entries {
			left = append(left, en.Name())
		}
		out := e.out.String()
		if dry {
			if len(left) != 3 {
				t.Errorf("the dry run removed something: %v", left)
			}
			if strings.Count(out, "would remove") != 2 {
				t.Errorf("the dry run did not list the two old backups:\n%s", out)
			}
			continue
		}
		if len(left) != 2 || left[0] != "apple-TESTSERIAL-2019-01-01.bin" || !strings.HasPrefix(left[1], "fwpart-") ||
			left[1] == old[0] || left[1] == old[1] {
			t.Errorf("left %v; want the Apple backup and the one just taken", left)
		}
		if strings.Count(out, "removed ") != 2 {
			t.Errorf("each removal was not said:\n%s", out)
		}
	}
}
