// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BrandonDedolph/core-desktop-app/internal/devicefs"
)

// writeBootLog replaces the volume's CORELOG.BIN with an 8-block log whose
// ring holds one block per boot id given, each a final disk-mode flush with
// the boot clock line the firmware prints.
func writeBootLog(t *testing.T, vol string, at time.Time, boots ...uint16) {
	t.Helper()
	const count = 8
	log := make([]byte, count*devicefs.LogBlockBytes)
	h := devicefs.EncodeLogHeader(count, 0x0BADCAFE)
	copy(log, h[:])
	for i, boot := range boots {
		text := "core: kernel alive (iPod 5G/5.5G, PP5022)\n" +
			"core: timesync host 00000000 off 00000000 applied 00000000 rtc " +
			hex8(uint32(at.Unix())) +
			" -> none rc 00000000\n" +
			"core: disk mode: entering\n"
		b, err := devicefs.EncodeLogBlock(uint32(i), boot, []byte(text), true)
		if err != nil {
			t.Fatal(err)
		}
		copy(log[int(devicefs.LogBlockIndex(uint32(i), count))*devicefs.LogBlockBytes:], b[:])
	}
	if err := os.WriteFile(filepath.Join(vol, devicefs.LogName), log, 0o644); err != nil {
		t.Fatal(err)
	}
}

func hex8(v uint32) string {
	const digits = "0123456789ABCDEF"
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = digits[v&0xF]
		v >>= 4
	}
	return string(b)
}

func noteFlash(t *testing.T, vol string, logBoot uint16) {
	t.Helper()
	_, err := devicefs.WriteFlashRecord(vol, devicefs.FlashRecord{
		Version: "v0.2.2", BuildID: "v0.2.2", Length: 237640, Checksum: 0x01589d64,
		Replaced: "Core v0.2.1 (build v0.2.1)", ReplacedVersion: "v0.2.1",
		Flashed:  time.Date(2026, 11, 1, 13, 55, 0, 0, time.UTC),
		LogKnown: true, LogID: 0x0BADCAFE, LogBoot: logBoot,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The bench check: `core doctor --boot` passes once the iPod has started
// the image the last flash wrote, and fails (exit 1) until then.
func TestDoctorBoot(t *testing.T) {
	at := time.Date(2026, 11, 1, 14, 3, 0, 0, time.UTC)
	cases := []struct {
		name    string
		setup   func(t *testing.T, vol string)
		fail    bool
		want    []string
		notWant []string
	}{
		{
			name: "booted the current image",
			setup: func(t *testing.T, vol string) {
				writeBootLog(t, vol, at, 57, 58)
				noteFlash(t, vol, 57)
			},
			want: []string{"OK    last boot    boot 58, started 2026-11-01 14:03 UTC",
				"OK    firmware     Core v0.2.2 — flashed 2026-11-01 13:55 UTC, booted since",
				"OK    ended        went into disk mode"},
		},
		{
			name: "booted the old image",
			setup: func(t *testing.T, vol string) {
				writeBootLog(t, vol, at, 57, 58)
				noteFlash(t, vol, 58)
			},
			fail: true,
			want: []string{"FAIL  firmware     flashed v0.2.2 on 2026-11-01 13:55 UTC, last boot ran v0.2.1 — the iPod has not restarted since"},
		},
		{
			name:  "never booted",
			setup: func(t *testing.T, vol string) { noteFlash(t, vol, 0) },
			fail:  true,
			want: []string{"WARN  last boot    the log holds no boot yet",
				"FAIL  firmware     flashed v0.2.2 on 2026-11-01 13:55 UTC, no boot logged since — the iPod has not started it yet"},
		},
		{
			name: "no log",
			setup: func(t *testing.T, vol string) {
				if err := os.Remove(filepath.Join(vol, devicefs.LogName)); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"WARN  last boot    CORELOG.BIN is missing",
				"WARN  firmware     no flash record (COREFLSH.TXT)"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vol := syntheticVolume(t, 12, nil)
			tc.setup(t, vol)
			out, _, err := runCore(t, "doctor", "--boot", "--volume", vol)
			t.Logf("\n%s", out)
			if (err != nil) != tc.fail {
				t.Errorf("err = %v, want failure %v", err, tc.fail)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q", w)
				}
			}
			// --boot prints the boot section only.
			for _, nw := range []string{"\nvolume", "\ndevice", "CORECFG.DAT valid"} {
				if strings.Contains(out, nw) {
					t.Errorf("--boot printed %q", nw)
				}
			}
		})
	}
}

// The full report carries the boot section after the volume section.
func TestDoctorPrintsTheBootSection(t *testing.T) {
	vol := syntheticVolume(t, 12, nil)
	out, _, _ := runCore(t, "doctor", "--volume", vol)
	v, b := strings.Index(out, "\nvolume"), strings.Index(out, "\nboot\n")
	if v < 0 || b < v {
		t.Errorf("no boot section after the volume section:\n%s", out)
	}
}
