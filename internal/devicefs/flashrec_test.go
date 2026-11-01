// SPDX-License-Identifier: Apache-2.0

package devicefs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFlashRecordRoundTrip(t *testing.T) {
	rec := FlashRecord{
		Version: "v0.2.2", BuildID: "v0.2.2-3-gabc1234", Length: 237640, Checksum: 0x01589d64,
		Replaced: "Core v0.2.1 (build v0.2.1)\n", ReplacedVersion: "v0.2.1",
		Flashed:  time.Date(2026, 11, 1, 13, 53, 0, 0, time.UTC),
		LogKnown: true, LogID: 0x0BADCAFE, LogBoot: 58,
	}
	got, err := ParseFlashRecord(rec.Encode())
	want := rec
	want.Replaced = "Core v0.2.1 (build v0.2.1)"
	if err != nil || got != want {
		t.Fatalf("round trip: %v\n got %+v\nwant %+v\n%s", err, got, want, rec.Encode())
	}
	noLog := FlashRecord{Length: 1, Checksum: 2}
	if got, err := ParseFlashRecord(noLog.Encode()); err != nil || got.LogKnown || got.Version != "" {
		t.Errorf("no log: %+v %v\n%s", got, err, noLog.Encode())
	}
	for name, text := range map[string]string{
		"empty":        "",
		"no checksum":  "length 1\nlog-id none\n",
		"bad number":   "length x\nchecksum 0x1\nlog-id none\n",
		"no log-boot":  "length 1\nchecksum 0x1\nlog-id 0x5\n",
		"bad flashed":  "length 1\nchecksum 0x1\nlog-id none\nflashed yesterday\n",
		"boot too big": "length 1\nchecksum 0x1\nlog-id 0x5\nlog-boot 70000\n",
	} {
		if _, err := ParseFlashRecord([]byte(text)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
	// A newer core's extra keys are not an error.
	if _, err := ParseFlashRecord([]byte("length 1\nchecksum 0x1\nlog-id none\nfuture yes\n")); err != nil {
		t.Errorf("unknown key: %v", err)
	}
}

func TestFlashRecordOnAVolume(t *testing.T) {
	vol := t.TempDir()
	if _, found, err := ReadFlashRecord(vol); found || err != nil {
		t.Fatalf("empty volume: %v %v", found, err)
	}
	rec := FlashRecord{Version: "v0.2.2", Length: 10, Checksum: 20, LogKnown: true, LogID: 1, LogBoot: 2}
	path, err := WriteFlashRecord(vol, rec)
	if err != nil || filepath.Base(path) != FlashRecordName {
		t.Fatalf("write: %s %v", path, err)
	}
	got, found, err := ReadFlashRecord(vol)
	if !found || err != nil || got != rec {
		t.Errorf("read back: %+v %v %v", got, found, err)
	}
	if err := os.WriteFile(path, []byte("junk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, found, err := ReadFlashRecord(vol); !found || err == nil {
		t.Errorf("junk record: found %v err %v", found, err)
	}
}
