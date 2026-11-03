// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BrandonDedolph/core-desktop-app/internal/devicefs"
)

// The fixtures are CORELOG.BIN images built with the firmware's block format
// (devicefs.EncodeLogBlock, which the devicefs parity test holds to
// tools/make_log.py) and text in the firmware's own line formats — each line
// below is one kernel/main.c prints, with its uart_put_hex32 fields as eight
// upper-case hex digits.

const fixtureLogID = 0x0BADCAFE

type blk struct {
	seq   uint32
	boot  uint16
	text  string
	final bool
}

// synthLog is make_log.py's synth(): a header for count blocks and each
// entry in the slot its seq lands in.
func synthLog(t *testing.T, count uint32, id uint32, entries ...blk) []byte {
	t.Helper()
	data := make([]byte, int(count)*devicefs.LogBlockBytes)
	h := devicefs.EncodeLogHeader(count, id)
	copy(data, h[:])
	for _, e := range entries {
		b, err := devicefs.EncodeLogBlock(e.seq, e.boot, []byte(e.text), e.final)
		if err != nil {
			t.Fatal(err)
		}
		off := int(devicefs.LogBlockIndex(e.seq, count)) * devicefs.LogBlockBytes
		copy(data[off:], b[:])
	}
	return data
}

func hex32(v uint32) string { return fmt.Sprintf("%08X", v) }

var bootAt = time.Date(2026, 11, 1, 14, 3, 0, 0, time.UTC)

// bootText is the first block of a boot, as the firmware narrates it, up to
// the evlog line (the first block is built at evlog_mount, so it always
// carries everything said before it).
func bootText(boot uint16, timesync string, extra ...string) string {
	l := []string{
		"core: kernel alive (iPod 5G/5.5G, PP5022)",
		"core: uart self-test 1234ABCD",
		"core: PROCESSOR_ID 55555555",
		"core: clock init -> 30 MHz",
		"core: cpu freq 01C9C380",
		"core: cache init",
		"core: timer init @ 100 Hz, enabling IRQs",
		"core: mount rc 00000000 BTUS 007A1200",
		"core: entering browser",
		"core: cfg load 00000001 writable 00000001 seq 0000002A lba 00012340/00012344",
		timesync,
	}
	l = append(l, extra...)
	l = append(l, "core: evlog on seq 00000010 boot "+hex32(uint32(boot))+
		" prev final lba 00020000/00020010",
		"core: otg load 0 writable 00000001 seq 00000003 lba 00030000/00030004")
	return strings.Join(l, "\n") + "\n"
}

func tsNone(rtc uint32) string {
	return "core: timesync host 6905A000 off FFFFFF88 applied 6905A000 rtc " + hex32(rtc) +
		" -> none rc 00000000"
}

func tsSet(host uint32) string {
	return "core: timesync host " + hex32(host) + " off FFFFFF88 applied " + hex32(host) +
		" rtc 00000000 -> set rc 00000000"
}

const diskMode = "core: batt raw 0312 mv 3987\ncore: disk mode: entering\n"

// named is the line firmware 39fdaf9 and later print after "kernel alive".
func named(tag string) string { return "core: version " + tag + " build " + tag + "-5-g39fdaf9" }

// counted is the line the same firmware prints once the library is in.
func counted(songs, albums int) string {
	return fmt.Sprintf("core: library songs %08X albums %08X ms 000004D2", songs, albums)
}

func TestLastBoot(t *testing.T) {
	at := uint32(bootAt.Unix())
	cases := []struct {
		name    string
		entries []blk
		want    BootFacts
		empty   bool
	}{
		{name: "empty ring", empty: true},
		{
			name: "one boot, one final block",
			entries: []blk{
				{0, 1, bootText(1, tsNone(at)) + diskMode, true},
			},
			want: BootFacts{ID: 1, FirstSeq: 0, LastSeq: 0, Blocks: 1, Start: true,
				At: bootAt, Final: true, End: "disk mode"},
		},
		{
			name: "newest of three boots, across two blocks; the clock set from the host",
			entries: []blk{
				{0, 1, bootText(1, tsNone(at-7200)), false},
				{1, 1, "core: suspend: entering\n", true},
				{2, 2, bootText(2, tsNone(at-3600)) + "core: standby: entering\n", true},
				{3, 3, bootText(3, tsSet(at)), false},
				{4, 3, "core: suspend: entering\n" + "core: suspend: wake\n" + diskMode, true},
			},
			want: BootFacts{ID: 3, FirstSeq: 3, LastSeq: 4, Blocks: 2, Start: true,
				At: bootAt, Final: true, End: "disk mode"},
		},
		{
			name: "the last exit named wins: woke, then slept again",
			entries: []blk{
				{7, 4, bootText(4, tsNone(at)) + "core: disk mode: entering\n", false},
				{8, 4, "core: suspend: entering\n", true},
			},
			want: BootFacts{ID: 4, FirstSeq: 7, LastSeq: 8, Blocks: 2, Start: true,
				At: bootAt, Final: true, End: "sleep"},
		},
		{
			name: "stopped: the newest block is an idle flush, not final",
			entries: []blk{
				{5, 9, bootText(9, tsNone(at)), false},
			},
			want: BootFacts{ID: 9, FirstSeq: 5, LastSeq: 5, Blocks: 1, Start: true, At: bootAt},
		},
		{
			name: "index refused, clock not valid",
			entries: []blk{
				{0, 2, bootText(2, tsNone(0), "idx: rejected (crc)"), true},
			},
			want: BootFacts{ID: 2, Blocks: 1, Start: true, IndexRejected: "crc", Final: true},
		},
		{
			name: "the ring overwrote the start of a long boot",
			entries: []blk{
				{20, 6, "core: batt raw 0300 mv 3900\n", false},
				{21, 6, "core: batt DISKSAFE: flushing settings, parking drive\n", true},
			},
			want: BootFacts{ID: 6, FirstSeq: 20, LastSeq: 21, Blocks: 2, Final: true,
				End: "low battery"},
		},
		{
			name: "a panic line, should a future firmware flush one",
			entries: []blk{
				{0, 1, bootText(1, tsNone(at)) + "\n*** PANIC: DATA ABORT (00000004)\n", false},
			},
			want: BootFacts{ID: 1, Blocks: 1, Start: true, At: bootAt, Panic: "DATA ABORT"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ring, err := devicefs.ReadLogRing(synthLog(t, 6, fixtureLogID, tc.entries...))
			if err != nil {
				t.Fatal(err)
			}
			got, ok := LastBoot(ring)
			if ok == tc.empty {
				t.Fatalf("ok = %v, want %v", ok, !tc.empty)
			}
			if got != tc.want {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestTimesyncTime(t *testing.T) {
	at := uint32(bootAt.Unix())
	cases := []struct {
		line string
		want time.Time
	}{
		{tsNone(at), bootAt},
		{tsSet(at), bootAt},
		{tsNone(0), time.Time{}},
		// A set whose RTC write failed leaves the RTC's own reading.
		{"core: timesync host " + hex32(at) + " off 00000000 applied 00000000 rtc " +
			hex32(at-60) + " -> set rc FFFFFFFB", bootAt.Add(-time.Minute)},
		{"core: timesync host 00000000 off 00000000 applied 00000000 rtc 00000000 -> none rc 00000000",
			time.Time{}},
	}
	for _, tc := range cases {
		got, _ := timesyncTime(tc.line)
		if !got.Equal(tc.want) {
			t.Errorf("%s\n got %v want %v", tc.line, got, tc.want)
		}
	}
}

func record(version, replaced string, logBoot uint16) devicefs.FlashRecord {
	return devicefs.FlashRecord{
		Version: version, BuildID: version, Length: 237640, Checksum: 0x01589d64,
		Replaced: "Core " + replaced, ReplacedVersion: replaced,
		Flashed:  bootAt.Add(-10 * time.Minute),
		LogKnown: true, LogID: fixtureLogID, LogBoot: logBoot,
	}
}

var image = ImageFacts{Known: true, Version: "v0.2.2", Length: 237640, Checksum: 0x01589d64}

func TestEvaluateBoot(t *testing.T) {
	at := uint32(bootAt.Unix())
	now := bootAt.Add(2*time.Hour + 5*time.Minute)
	twoBoots := func(t *testing.T) []byte {
		return synthLog(t, 2048, fixtureLogID,
			blk{0, 57, bootText(57, tsNone(at-86400)) + diskMode, true},
			blk{1, 58, bootText(58, tsNone(at)) + diskMode, true})
	}
	cases := []struct {
		name  string
		in    func(t *testing.T) BootInputs
		state BootState
		ver   string
		want  []string // "STATE name: text-fragment"
	}{
		{
			name: "the boot named its build, and it is the image on the iPod",
			in: func(t *testing.T) BootInputs {
				log := synthLog(t, 2048, fixtureLogID,
					blk{0, 58, bootText(58, tsNone(at), named("v0.2.2"), counted(907, 101)) + diskMode, true})
				// A stale record (boot 58 noted as the newest) would say "not
				// yet"; the iPod's own line wins.
				return BootInputs{Log: log, Record: record("v0.2.2", "v0.2.1", 58),
					RecordFound: true, Image: image, IndexOK: true, Songs: 907, Now: now}
			},
			state: BootCurrent, ver: "v0.2.2",
			want: []string{
				"OK firmware: Core v0.2.2 — the iPod said so at its last boot, and that is the image it holds",
				"OK index: the iPod counted 907 songs in 101 albums at that boot",
			},
		},
		{
			name: "the boot named an older build than the image on the iPod",
			in: func(t *testing.T) BootInputs {
				log := synthLog(t, 2048, fixtureLogID,
					blk{0, 58, bootText(58, tsNone(at), named("v0.2.1"), counted(900, 100)) + diskMode, true})
				return BootInputs{Log: log, Image: image, IndexOK: true, Songs: 907, Now: now}
			},
			state: BootNotYet, ver: "v0.2.1",
			want: []string{
				"FAIL firmware: last boot ran v0.2.1, the iPod holds v0.2.2 — it has not restarted since that was written",
				"WARN index: the iPod counted 900 songs at that boot; CORELIB.IDX now holds 907",
			},
		},
		{
			name: "the boot named its build; no image read, no record",
			in: func(t *testing.T) BootInputs {
				log := synthLog(t, 2048, fixtureLogID,
					blk{0, 58, bootText(58, tsNone(at), named("v0.2.2")) + diskMode, true})
				return BootInputs{Log: log, IndexOK: true, Songs: 907, Now: now}
			},
			state: BootCurrent, ver: "v0.2.2",
			want: []string{
				"OK firmware: Core v0.2.2 — the iPod said so at its last boot (nothing on the host says",
				"OK index: the library loaded from CORELIB.IDX, which holds 907 songs",
			},
		},
		{
			name: "booted the current image",
			in: func(t *testing.T) BootInputs {
				return BootInputs{Log: twoBoots(t), Record: record("v0.2.2", "v0.2.1", 57),
					RecordFound: true, Image: image, IndexOK: true, Songs: 907, Now: now}
			},
			state: BootCurrent, ver: "v0.2.2",
			want: []string{
				"OK last boot: boot 58, started 2026-11-01 14:03 UTC (2 hours ago)",
				"OK firmware: Core v0.2.2 — flashed 2026-11-01 13:53 UTC, booted since",
				"OK index: the library loaded from CORELIB.IDX, which holds 907 songs",
				"OK ended: went into disk mode",
			},
		},
		{
			name: "flashed after the newest boot: still running the old one",
			in: func(t *testing.T) BootInputs {
				return BootInputs{Log: twoBoots(t), Record: record("v0.2.2", "v0.2.1", 58),
					RecordFound: true, Image: image, IndexOK: true, Songs: 907, Now: now}
			},
			state: BootNotYet, ver: "v0.2.1",
			want: []string{
				"OK last boot: boot 58",
				"FAIL firmware: flashed v0.2.2 on 2026-11-01 13:53 UTC, last boot ran v0.2.1 — the iPod has not restarted since",
			},
		},
		{
			name: "first install: a log with no boot in it",
			in: func(t *testing.T) BootInputs {
				return BootInputs{Log: synthLog(t, 2048, fixtureLogID),
					Record: record("v0.2.2", "", 0), RecordFound: true, Image: image, Now: now}
			},
			state: BootNotYet,
			want: []string{
				"WARN last boot: the log holds no boot yet",
				"FAIL firmware: flashed v0.2.2 on 2026-11-01 13:53 UTC, no boot logged since — the iPod has not started it yet",
			},
		},
		{
			name: "no log file at all",
			in: func(t *testing.T) BootInputs {
				return BootInputs{LogErr: os.ErrNotExist, Record: record("v0.2.2", "v0.2.1", 0),
					RecordFound: true, Image: image, Now: now}
			},
			state: BootNoLog,
			want: []string{
				"WARN last boot: CORELOG.BIN is missing",
				"WARN firmware: flashed v0.2.2 on 2026-11-01 13:53 UTC; with no log, nothing says whether the iPod has started it",
			},
		},
		{
			name: "the log was recreated after the write: its boots are newer",
			in: func(t *testing.T) BootInputs {
				rec := record("v0.2.2", "v0.2.1", 900)
				rec.LogID = 0x12345678
				return BootInputs{Log: twoBoots(t), Record: rec, RecordFound: true,
					Image: image, IndexOK: true, Now: now}
			},
			state: BootCurrent, ver: "v0.2.2",
			want: []string{"OK firmware: Core v0.2.2"},
		},
		{
			name: "no valid log at the write (install), one boot since",
			in: func(t *testing.T) BootInputs {
				rec := record("v0.2.2", "", 0)
				rec.LogKnown, rec.LogID = false, 0
				return BootInputs{Log: twoBoots(t), Record: rec, RecordFound: true,
					Image: image, IndexOK: true, Now: now}
			},
			state: BootCurrent, ver: "v0.2.2",
		},
		{
			name: "boot ids wrapped past 65535",
			in: func(t *testing.T) BootInputs {
				log := synthLog(t, 64, fixtureLogID, blk{9, 2, bootText(2, tsNone(at)), true})
				return BootInputs{Log: log, Record: record("v0.2.2", "v0.2.1", 65534),
					RecordFound: true, Image: image, Now: now}
			},
			state: BootCurrent, ver: "v0.2.2",
		},
		{
			name: "no flash record: a boot, build unknown",
			in: func(t *testing.T) BootInputs {
				return BootInputs{Log: twoBoots(t), Image: image, IndexOK: true, Now: now}
			},
			state: BootUnknownBuild,
			want:  []string{"WARN firmware: no flash record (COREFLSH.TXT): this image was written by an older core or another tool, so nothing says which build the last boot ran; the iPod holds Core v0.2.2"},
		},
		{
			name: "the record names another image",
			in: func(t *testing.T) BootInputs {
				img := image
				img.Checksum = 0xdeadbeef
				return BootInputs{Log: twoBoots(t), Record: record("v0.2.2", "v0.2.1", 57),
					RecordFound: true, Image: img, Now: now}
			},
			state: BootUnknownBuild,
			want:  []string{"WARN firmware: COREFLSH.TXT describes another image"},
		},
		{
			name: "an unreadable record",
			in: func(t *testing.T) BootInputs {
				return BootInputs{Log: twoBoots(t), RecordFound: true,
					RecordErr: errors.New("bad"), Image: image, Now: now}
			},
			state: BootUnknownBuild,
			want:  []string{"WARN firmware: COREFLSH.TXT could not be read (bad)"},
		},
		{
			name: "image not read (no elevation): the record is taken as it stands",
			in: func(t *testing.T) BootInputs {
				return BootInputs{Log: twoBoots(t), Record: record("v0.2.2", "v0.2.1", 57),
					RecordFound: true, Now: now}
			},
			state: BootCurrent, ver: "v0.2.2",
			want: []string{"OK firmware: Core v0.2.2 — flashed 2026-11-01 13:53 UTC, booted since (the image on the iPod was not read to confirm it)"},
		},
		{
			name: "a header that does not validate",
			in: func(t *testing.T) BootInputs {
				return BootInputs{Log: make([]byte, 4096), Now: now}
			},
			state: BootNoLog,
			want:  []string{"WARN last boot: CORELOG.BIN has no valid header"},
		},
		{
			name: "stopped without a clean shutdown, index refused",
			in: func(t *testing.T) BootInputs {
				log := synthLog(t, 16, fixtureLogID,
					blk{3, 5, bootText(5, tsNone(at), "idx: rejected (crc)"), false})
				return BootInputs{Log: log, Record: record("v0.2.2", "v0.2.1", 4),
					RecordFound: true, Image: image, Now: now}
			},
			state: BootCurrent, ver: "v0.2.2",
			want: []string{
				"WARN index: the iPod refused CORELIB.IDX (crc) at that boot",
				"WARN ended: without a clean shutdown",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := EvaluateBoot(tc.in(t))
			if rep.State != tc.state {
				t.Errorf("state = %d, want %d", rep.State, tc.state)
			}
			if rep.Version != tc.ver {
				t.Errorf("version = %q, want %q", rep.Version, tc.ver)
			}
			var got []string
			for _, c := range rep.Checks {
				got = append(got, fmt.Sprintf("%s %s: %s", c.State, c.Name, c.Text))
			}
			for _, w := range tc.want {
				found := false
				for _, g := range got {
					if strings.HasPrefix(g, w) {
						found = true
					}
				}
				if !found {
					t.Errorf("no line starting %q in\n  %s", w, strings.Join(got, "\n  "))
				}
			}
		})
	}
}

func TestBootStateCounts(t *testing.T) {
	// --boot's exit status is "any FAIL": only the not-yet-booted verdict
	// (and a log that cannot be read at all) may produce one.
	at := uint32(bootAt.Unix())
	log := synthLog(t, 8, fixtureLogID, blk{0, 3, bootText(3, tsNone(at)) + diskMode, true})
	ok := EvaluateBoot(BootInputs{Log: log, Record: record("v0.2.2", "v0.2.1", 2),
		RecordFound: true, Image: image})
	if n := Counts(ok.Checks)[Fail]; n != 0 {
		t.Errorf("booted current image: %d FAIL", n)
	}
	stale := EvaluateBoot(BootInputs{Log: log, Record: record("v0.2.2", "v0.2.1", 3),
		RecordFound: true, Image: image})
	if n := Counts(stale.Checks)[Fail]; n != 1 {
		t.Errorf("not booted: %d FAIL, want 1", n)
	}
}

func TestCheckBootReadsTheVolume(t *testing.T) {
	vol := t.TempDir()
	at := uint32(bootAt.Unix())
	log := synthLog(t, 8, fixtureLogID, blk{0, 3, bootText(3, tsNone(at)) + diskMode, true})
	if err := os.WriteFile(vol+"/"+devicefs.LogName, log, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := devicefs.WriteFlashRecord(vol, record("v0.2.2", "v0.2.1", 2)); err != nil {
		t.Fatal(err)
	}
	rep := CheckBoot(vol, DeviceReport{Image: image}, VolumeReport{IndexOK: true, Songs: 12}, bootAt)
	if rep.State != BootCurrent || rep.Version != "v0.2.2" {
		t.Errorf("state %d version %q", rep.State, rep.Version)
	}
	if rep := CheckBoot("", DeviceReport{}, VolumeReport{}, bootAt); len(rep.Checks) != 1 ||
		rep.Checks[0].State != Skip {
		t.Errorf("no volume: %+v", rep.Checks)
	}
}

// Boots reads EVERY boot with the parser LastBoot uses, not only the newest:
// three boots of three builds and three clocks come back newest first, each
// with its own build, time, end and lines.
func TestBootsEachBootParsed(t *testing.T) {
	at := uint32(bootAt.Unix())
	ring, err := devicefs.ReadLogRing(synthLog(t, 8, fixtureLogID,
		blk{0, 1, bootText(1, tsNone(at-7200), named("v0.2.0")), false},
		blk{1, 1, "core: suspend: entering\n", true},
		blk{2, 2, bootText(2, tsSet(at-3600), named("v0.2.1"), counted(907, 101)), false},
		blk{3, 2, "core: standby: entering\n", true},
		blk{4, 3, bootText(3, tsNone(at), named("v0.2.2")), false},
	))
	if err != nil {
		t.Fatal(err)
	}
	boots := Boots(ring)
	if len(boots) != 3 {
		t.Fatalf("%d boots, want 3", len(boots))
	}
	want := []struct {
		id      uint16
		version string
		at      time.Time
		end     string
		final   bool
		seqs    []uint32
	}{
		{3, "v0.2.2", bootAt, "", false, []uint32{4}},
		{2, "v0.2.1", bootAt.Add(-time.Hour), "standby", true, []uint32{2, 3}},
		{1, "v0.2.0", bootAt.Add(-2 * time.Hour), "sleep", true, []uint32{0, 1}},
	}
	for i, w := range want {
		b := boots[i]
		f := b.Facts
		if f.ID != w.id || f.Version != w.version || f.BuildID != w.version+"-5-g39fdaf9" ||
			!f.At.Equal(w.at) || f.End != w.end || f.Final != w.final || !f.Start {
			t.Errorf("boot %d facts = %+v", w.id, f)
		}
		if len(b.Entries) != len(w.seqs) {
			t.Fatalf("boot %d: %d entries, want %d", w.id, len(b.Entries), len(w.seqs))
		}
		for j, e := range b.Entries {
			if e.Seq != w.seqs[j] || e.Boot != w.id {
				t.Errorf("boot %d entry %d = seq %d boot %d", w.id, j, e.Seq, e.Boot)
			}
		}
	}
	if f := boots[1].Facts; !f.Counted || f.Songs != 907 || f.Albums != 101 {
		t.Errorf("boot 2's library line was not read: %+v", f)
	}
	if last, _ := LastBoot(ring); last != boots[0].Facts {
		t.Errorf("LastBoot = %+v, Boots[0] = %+v", last, boots[0].Facts)
	}
	if got := Boots(devicefs.LogRing{}); len(got) != 0 {
		t.Errorf("an empty ring has %d boots", len(got))
	}
}
