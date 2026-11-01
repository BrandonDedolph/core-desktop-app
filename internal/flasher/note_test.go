// SPDX-License-Identifier: Apache-2.0

package flasher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BrandonDedolph/core-desktop-app/internal/devicefs"
	"github.com/BrandonDedolph/core-desktop-app/internal/fwpart"
)

// After a verified image write the flasher leaves COREFLSH.TXT on the music
// volume: the written row, the marker, what it replaced, and the log's newest
// boot id at that moment — the numbers `core doctor --boot` compares with.
func TestFlashNotesTheWriteOnTheVolume(t *testing.T) {
	e := newEnv(t, syntheticPartition(t, plausibleImage(8192, 1), plausibleImage(4096, 2)))
	vol := t.TempDir()
	e.pod.Disk.MountPoints = []string{vol}

	// A log whose newest block is boot 41.
	const count = 8
	log := make([]byte, count*devicefs.LogBlockBytes)
	h := devicefs.EncodeLogHeader(count, 0x0BADCAFE)
	copy(log, h[:])
	b, _ := devicefs.EncodeLogBlock(3, 41, []byte("core: disk mode: entering\n"), true)
	copy(log[int(devicefs.LogBlockIndex(3, count))*devicefs.LogBlockBytes:], b[:])
	if err := os.WriteFile(filepath.Join(vol, devicefs.LogName), log, 0o644); err != nil {
		t.Fatal(err)
	}

	image := append(plausibleImage(9000, 4), []byte("\x00\x00\x00\x00CORE-FW-VERSION:v0.2.2|v0.2.2-1-gabc1234\x00")...)
	path := writeTemp(t, t.TempDir(), "core.bin", image)
	res, err := Flash(context.Background(),
		Options{Image: path, Yes: true, BackupDir: e.backupDir}, e.deps())
	if err != nil || !res.Verified {
		t.Fatalf("Flash: %v\n%s", err, e.out.String())
	}

	rec, found, err := devicefs.ReadFlashRecord(vol)
	if !found || err != nil {
		t.Fatalf("no record: %v %v\n%s", found, err, e.out.String())
	}
	want := devicefs.FlashRecord{
		Version: "v0.2.2", BuildID: "v0.2.2-1-gabc1234",
		Length: uint32(len(image)), Checksum: fwpart.ImageChecksum(image),
		Replaced: res.Installed.Description, ReplacedVersion: res.Installed.Version,
		Flashed:  time.Date(2026, 9, 15, 1, 2, 3, 0, time.UTC),
		LogKnown: true, LogID: 0x0BADCAFE, LogBoot: 41,
	}
	if rec != want {
		t.Errorf("record\n got %+v\nwant %+v", rec, want)
	}
	if !strings.Contains(e.out.String(), "core doctor --boot") {
		t.Errorf("the output does not say how to check the boot:\n%s", e.out.String())
	}
}

// No mounted volume is not a failed flash: one line says the note was not
// left, and nothing is written anywhere.
func TestFlashWithoutAVolumeSaysSo(t *testing.T) {
	e := newEnv(t, syntheticPartition(t, plausibleImage(8192, 1), plausibleImage(4096, 2)))
	e.pod.Disk.MountPoints, e.pod.Disk.Volumes = nil, nil
	called := false
	d := e.deps()
	d.NoteFlash = func(string, devicefs.FlashRecord) (string, error) { called = true; return "", nil }
	path := writeTemp(t, t.TempDir(), "core.bin", plausibleImage(9000, 4))
	res, err := Flash(context.Background(),
		Options{Image: path, Yes: true, BackupDir: e.backupDir}, d)
	if err != nil || !res.Verified {
		t.Fatalf("Flash: %v", err)
	}
	if called {
		t.Error("a record was written with no volume")
	}
	if !strings.Contains(e.out.String(), "boot check   not noted") {
		t.Errorf("no 'not noted' line:\n%s", e.out.String())
	}
}
