// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BrandonDedolph/core-desktop-app/internal/devicefs"
)

// Boot evidence: did the iPod actually start the firmware that was flashed?
//
// A flash ends with the image read back byte for byte, and that proves the
// bytes, not the boot. What proves the boot is the device's own event log,
// CORELOG.BIN (core/kernel/evlog.c): every block the firmware writes carries a
// boot id, and the text is the UART narration, starting at "core: kernel
// alive". The firmware does NOT print its version there, so the log alone
// cannot say which build ran. The flash record (devicefs.FlashRecord, written
// by the flasher) closes that gap: it notes the log's newest boot id at the
// moment of the write, so a newer boot id in the log now is a boot of the
// written image, and no newer boot id means the iPod has not started it.
//
// WHAT A LOGGED BOOT ALREADY IMPLIES. evlog_mount() runs in run_ui() after
// the volume is mounted and after library_ensure(), so a boot with any block
// on disk got at least that far: it mounted the music volume and loaded the
// library (from the index, or from a tag scan if it refused the index, which
// it says with an "idx: rejected (...)" line). The blocks reach the disk only
// on a forced flush (sleep, standby, disk mode, the low-battery edge) or when
// a whole 2 KB block has filled, so a boot that crashed in its first minute
// can leave nothing — which reads here as "no boot since", and is reported
// as such rather than as a boot.
//
// A panic halts without flushing (core/kernel/panic.c talks to the UART
// only), so a "*** PANIC:" line reaches the disk only if some later firmware
// learns to flush it. The parser looks for it anyway; what the log DOES
// always show is a session whose newest block is not FINAL — one that just
// stopped (a reset, a crash, a flat battery) instead of going down through
// a path that flushes.

// BootFacts is what the newest boot in a log says about itself. Every field
// is parsed out of the firmware's own lines; nothing is inferred from the
// host's side.
type BootFacts struct {
	ID                uint16
	FirstSeq, LastSeq uint32
	Blocks            int
	// Start: the boot's first line ("core: kernel alive") is still in
	// the ring. False when the ring has overwritten it.
	Start bool
	// At is the wall-clock time the iPod's clock read at boot (UTC), or
	// the host stamp it applied then; zero when the clock line is absent
	// or the clock was not valid.
	At time.Time
	// IndexRejected is the reason in "idx: rejected (<why>)", or "".
	IndexRejected string
	// Panic is the cause in "*** PANIC: <cause>", or "".
	Panic string
	// Final: the newest block of this boot was a forced flush — the
	// session ended through a path that writes (or is in one now).
	Final bool
	// End is the last of those paths the text names: "disk mode",
	// "sleep", "standby", "low battery", "battery empty"; "" when none.
	End string
	// Version and BuildID are what the boot said of itself in
	// "core: version <tag> build <id>" (firmware 39fdaf9 and later); ""
	// for older firmware, whose log never named its build.
	Version, BuildID string
	// Counted: the boot printed "core: library songs N albums M ms T"
	// once the library was in, and Songs/Albums hold those counts.
	Counted       bool
	Songs, Albums int
}

// The firmware's lines this file reads, exactly as kernel/main.c and
// kernel/panic.c print them.
const (
	lineAlive    = "core: kernel alive"
	lineTimesync = "core: timesync host "
	lineIdxRej   = "idx: rejected ("
	linePanic    = "*** PANIC: "
	lineVersion  = "core: version "
	lineLibrary  = "core: library songs "
)

// endLines maps the narration of each flushing exit to the words used for it.
var endLines = []struct{ line, words string }{
	{"core: disk mode: entering", "disk mode"},
	{"core: suspend: entering", "sleep"},
	{"core: standby: entering", "standby"},
	{"core: batt DISKSAFE", "low battery"},
	{"core: batt SHUTOFF", "battery empty"},
}

// LastBoot picks the newest boot out of a decoded ring: the run of blocks at
// the end that carry the newest block's boot id. ok is false for an empty
// ring.
func LastBoot(r devicefs.LogRing) (BootFacts, bool) {
	newest, ok := r.Newest()
	if !ok {
		return BootFacts{}, false
	}
	i := len(r.Entries)
	for i > 0 && r.Entries[i-1].Boot == newest.Boot {
		i--
	}
	return factsOf(r.Entries[i:]), true
}

// BootLog is one boot's share of the log: what its lines say about it, and
// the lines themselves in the dump tool's order and decoding.
type BootLog struct {
	Facts   BootFacts
	Entries []devicefs.LogEntry
}

// Boots splits a decoded ring into its boots, NEWEST FIRST, each one a run of
// consecutive blocks with the same boot id — the runs the dump tool marks
// with "=== boot N ===". Every boot's facts come from the same parser as
// LastBoot's, so the newest one here is LastBoot's answer. Empty for an empty
// ring.
func Boots(r devicefs.LogRing) []BootLog {
	ents := devicefs.OrderedLogEntries(r)
	var out []BootLog
	for end := len(r.Entries); end > 0; {
		start := end - 1
		for start > 0 && r.Entries[start-1].Boot == r.Entries[end-1].Boot {
			start--
		}
		out = append(out, BootLog{Facts: factsOf(r.Entries[start:end]), Entries: ents[start:end]})
		end = start
	}
	return out
}

// factsOf parses one boot's run of blocks (non-empty, one boot id).
func factsOf(run []devicefs.LogBlock) BootFacts {
	last := run[len(run)-1]
	var text bytes.Buffer
	for _, b := range run {
		text.Write(b.Text)
		// A drop or a missing block between two blocks is a cut, not a
		// join: never glue two halves into a line neither block said.
		if n := len(b.Text); n > 0 && b.Text[n-1] != '\n' {
			text.WriteByte('\n')
		}
	}
	f := BootFacts{
		ID:       last.Boot,
		FirstSeq: run[0].Seq,
		LastSeq:  last.Seq,
		Blocks:   len(run),
		Final:    last.Final,
	}
	parseBootText(&f, text.String())
	return f
}

func parseBootText(f *BootFacts, s string) {
	endAt := -1
	for off, line := range lines(s) {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, lineAlive):
			f.Start = true
		case strings.HasPrefix(line, lineTimesync):
			if t, ok := timesyncTime(line); ok {
				f.At = t
			}
		case strings.HasPrefix(line, lineIdxRej):
			why := strings.TrimPrefix(line, lineIdxRej)
			f.IndexRejected = strings.TrimSuffix(why, ")")
		case strings.HasPrefix(line, lineVersion):
			// "core: version v0.2.2 build v0.2.2-5-g39fdaf9"
			if w := strings.Fields(line); len(w) >= 3 {
				f.Version = w[2]
				if len(w) >= 5 && w[3] == "build" {
					f.BuildID = w[4]
				}
			}
		case strings.HasPrefix(line, lineLibrary):
			// "core: library songs 0000038B albums 00000065 ms 00000ABC"
			if w := strings.Fields(line); len(w) >= 6 && w[4] == "albums" {
				songs, err1 := strconv.ParseUint(w[3], 16, 32)
				albums, err2 := strconv.ParseUint(w[5], 16, 32)
				if err1 == nil && err2 == nil {
					f.Counted, f.Songs, f.Albums = true, int(songs), int(albums)
				}
			}
		case strings.HasPrefix(line, linePanic):
			f.Panic = strings.TrimSpace(strings.TrimPrefix(line, linePanic))
			if i := strings.Index(f.Panic, " ("); i > 0 {
				f.Panic = f.Panic[:i]
			}
		}
		for _, e := range endLines {
			if strings.HasPrefix(line, e.line) && off > endAt {
				endAt, f.End = off, e.words
			}
		}
	}
}

// lines splits on '\n' keeping the index order; the int is the line number.
func lines(s string) []string { return strings.Split(s, "\n") }

// timesyncTime reads the boot's wall clock out of
//
//	core: timesync host HHHHHHHH off OOOOOOOO applied AAAAAAAA rtc RRRRRRRR -> set rc CCCCCCCC
//
// (kernel/main.c, run_ui's boot block). After "-> set" with rc 0 the clock
// IS the host stamp; otherwise it is whatever the RTC read, 0 meaning the
// chip's time was not valid. The RTC keeps UTC (hal_rtc_set takes the
// host's UTC epoch).
func timesyncTime(line string) (time.Time, bool) {
	f := strings.Fields(line)
	get := func(key string) (uint32, bool) {
		for i := 0; i+1 < len(f); i++ {
			if f[i] == key {
				v, err := strconv.ParseUint(f[i+1], 16, 32)
				return uint32(v), err == nil
			}
		}
		return 0, false
	}
	host, okH := get("host")
	rtc, okR := get("rtc")
	rc, _ := get("rc")
	set := strings.Contains(line, "-> set")
	switch {
	case set && okH && rc == 0 && host != 0:
		return time.Unix(int64(host), 0).UTC(), true
	case okR && rtc != 0:
		return time.Unix(int64(rtc), 0).UTC(), true
	}
	return time.Time{}, false
}

// BootState is the one-word answer the app's line is built on.
type BootState int

// The answers.
const (
	// BootNoLog: there is no log to read (missing, or no valid header).
	BootNoLog BootState = iota
	// BootNone: the log is valid and holds no boot at all.
	BootNone
	// BootUnknownBuild: a boot is logged, but nothing says which build
	// ran it (no flash record, or one for a different image).
	BootUnknownBuild
	// BootCurrent: the iPod has booted since the image on it was written.
	BootCurrent
	// BootNotYet: the image on it was written after the newest boot.
	BootNotYet
)

// ImageFacts is the OSOS row as the device half read it.
type ImageFacts struct {
	Known            bool
	Version          string
	Length, Checksum uint32
}

// BootInputs is everything the verdict looks at, already read. EvaluateBoot
// is a pure function of it, so the tests drive every branch with bytes.
type BootInputs struct {
	// Log is CORELOG.BIN's contents; LogErr is the error reading it
	// (os.ErrNotExist for a missing file).
	Log    []byte
	LogErr error
	// Record is COREFLSH.TXT; RecordFound says there was one; RecordErr
	// is a read or parse error.
	Record      devicefs.FlashRecord
	RecordFound bool
	RecordErr   error
	Image       ImageFacts
	// IndexOK and Songs are the volume half's view of CORELIB.IDX now.
	IndexOK bool
	Songs   int
	Now     time.Time
}

// BootReport is the boot section.
type BootReport struct {
	State BootState
	// Version is the build the newest boot ran, when it is known.
	Version string
	// Flashed is the version the flash record names (for BootNotYet).
	Flashed string
	Boot    BootFacts
	Booted  bool
	Checks  []Check
}

// EvaluateBoot turns the inputs into the section's lines and the verdict.
func EvaluateBoot(in BootInputs) BootReport {
	r := &report{}
	var rep BootReport

	var ring devicefs.LogRing
	logOK := false
	switch {
	case errors.Is(in.LogErr, os.ErrNotExist):
		r.line(Warn, "last boot", "%s is missing, so nothing records a boot (run `core sync`)",
			devicefs.LogName)
	case in.LogErr != nil:
		r.line(Fail, "last boot", "%s could not be read: %v", devicefs.LogName, in.LogErr)
	default:
		var err error
		if ring, err = devicefs.ReadLogRing(in.Log); err != nil {
			r.line(Warn, "last boot", "%s has no valid header, so nothing records a boot",
				devicefs.LogName)
		} else {
			logOK = true
		}
	}

	var boot BootFacts
	if logOK {
		rep.State = BootNone
		boot, rep.Booted = LastBoot(ring)
		rep.Boot = boot
		if !rep.Booted {
			r.line(Warn, "last boot", "the log holds no boot yet — the iPod writes it when "+
				"it sleeps, switches off or goes into disk mode")
		} else {
			r.line(OK, "last boot", "%s", bootSentence(boot, in.Now))
		}
	}

	rep.Flashed = versionWords(in.Record.Version)
	if rep.Booted && boot.Version != "" {
		firmwareFromLog(r, &rep, boot, in)
	} else {
		since, sinceKnown := false, false
		switch {
		case in.RecordErr != nil:
			r.line(Warn, "firmware", "%s could not be read (%v); cannot tell which build "+
				"the last boot ran", devicefs.FlashRecordName, in.RecordErr)
		case !in.RecordFound:
			r.line(Warn, "firmware", "no flash record (%s): this image was written by an "+
				"older core or another tool, so nothing says which build the last boot "+
				"ran%s", devicefs.FlashRecordName, imageTail(in.Image))
		case in.Image.Known && (in.Image.Length != in.Record.Length ||
			in.Image.Checksum != in.Record.Checksum):
			r.line(Warn, "firmware", "%s describes another image (%d bytes, checksum %#08x) "+
				"than the one on the iPod (%d bytes, checksum %#08x); cannot tell which "+
				"build the last boot ran", devicefs.FlashRecordName, in.Record.Length,
				in.Record.Checksum, in.Image.Length, in.Image.Checksum)
		case !logOK:
			r.line(Warn, "firmware", "flashed %s on %s; with no log, nothing says whether "+
				"the iPod has started it", rep.Flashed, stamp(in.Record.Flashed))
		default:
			since, sinceKnown = bootedSince(in.Record, ring, rep.Booted, boot), true
		}
		unchecked := ""
		if !in.Image.Known {
			unchecked = " (the image on the iPod was not read to confirm it)"
		}
		switch {
		case sinceKnown && since:
			rep.State, rep.Version = BootCurrent, rep.Flashed
			r.line(OK, "firmware", "Core %s — flashed %s, booted since%s",
				rep.Flashed, stamp(in.Record.Flashed), unchecked)
		case sinceKnown:
			rep.State = BootNotYet
			if was := versionWords(in.Record.ReplacedVersion); rep.Booted && in.Record.ReplacedVersion != "" {
				rep.Version = in.Record.ReplacedVersion
				r.line(Fail, "firmware", "flashed %s on %s, last boot ran %s — the iPod has "+
					"not restarted since%s", rep.Flashed, stamp(in.Record.Flashed), was, unchecked)
			} else {
				r.line(Fail, "firmware", "flashed %s on %s, no boot logged since — the iPod "+
					"has not started it yet%s", rep.Flashed, stamp(in.Record.Flashed), unchecked)
			}
		case rep.Booted:
			rep.State = BootUnknownBuild
		}
	}

	if rep.Booted {
		switch {
		case boot.IndexRejected != "":
			r.line(Warn, "index", "the iPod refused %s (%s) at that boot and scanned the "+
				"tags instead — run `core index`", devicefs.IndexName, boot.IndexRejected)
		case boot.Counted && in.IndexOK && boot.Songs != in.Songs:
			r.line(Warn, "index", "the iPod counted %d songs at that boot; %s now holds %d — "+
				"it has not restarted since that index was written", boot.Songs,
				devicefs.IndexName, in.Songs)
		case boot.Counted:
			r.line(OK, "index", "the iPod counted %d songs in %d albums at that boot",
				boot.Songs, boot.Albums)
		case in.IndexOK:
			r.line(OK, "index", "the library loaded from %s, which holds %d songs",
				devicefs.IndexName, in.Songs)
		default:
			r.line(OK, "index", "the library loaded (%s is not valid now; see the volume "+
				"section)", devicefs.IndexName)
		}
		switch {
		case boot.Panic != "":
			r.line(Fail, "ended", "panicked: %s", boot.Panic)
		case boot.Final && boot.End != "":
			r.line(OK, "ended", "%s", endSentence(boot.End))
		case boot.Final:
			r.line(OK, "ended", "with a clean flush of the log")
		default:
			r.line(Warn, "ended", "without a clean shutdown — a reset, a crash or a flat "+
				"battery (its last log block is not a final flush)")
		}
	}
	rep.Checks = r.checks
	return rep
}

// firmwareFromLog is the firmware line when the boot named its own build:
// the iPod's own word, which outranks anything inferred from the flash
// record. Firmware before 39fdaf9 never printed it, and those logs take
// the record path in EvaluateBoot.
func firmwareFromLog(r *report, rep *BootReport, b BootFacts, in BootInputs) {
	rep.Version = b.Version
	ran := versionWords(b.Version)
	switch {
	case in.Image.Known && in.Image.Version != "":
		if in.Image.Version == b.Version {
			rep.State = BootCurrent
			r.line(OK, "firmware", "Core %s — the iPod said so at its last boot, and that "+
				"is the image it holds", ran)
			return
		}
		rep.State, rep.Flashed = BootNotYet, versionWords(in.Image.Version)
		r.line(Fail, "firmware", "last boot ran %s, the iPod holds %s — it has not "+
			"restarted since that was written", ran, rep.Flashed)
	case in.RecordFound && in.Record.Version != "":
		if in.Record.Version == b.Version {
			rep.State = BootCurrent
			r.line(OK, "firmware", "Core %s — the iPod said so at its last boot; flashed %s "+
				"(the image on the iPod was not read to confirm it)", ran,
				stamp(in.Record.Flashed))
			return
		}
		rep.State = BootNotYet
		r.line(Fail, "firmware", "last boot ran %s, flashed %s on %s — the iPod has not "+
			"restarted since (the image on the iPod was not read to confirm it)", ran,
			rep.Flashed, stamp(in.Record.Flashed))
	default:
		rep.State = BootCurrent
		r.line(OK, "firmware", "Core %s — the iPod said so at its last boot (nothing on "+
			"the host says whether a newer image was written since)", ran)
	}
}

// bootedSince is the comparison the flash record exists for.
func bootedSince(rec devicefs.FlashRecord, ring devicefs.LogRing, booted bool, b BootFacts) bool {
	if !booted {
		return false
	}
	if !rec.LogKnown || ring.FileID != rec.LogID {
		// No valid log at the write, or this one was made after it:
		// every boot in it came later.
		return true
	}
	// Boot ids are u16 and step by one per boot; a signed difference
	// keeps a wrap from reading as 65 000 boots ago.
	return int16(b.ID-rec.LogBoot) > 0
}

func bootSentence(b BootFacts, now time.Time) string {
	s := fmt.Sprintf("boot %d", b.ID)
	switch {
	case !b.At.IsZero():
		s += ", started " + stamp(b.At)
		if !now.IsZero() && !now.Before(b.At) {
			s += " (" + ago(now.Sub(b.At)) + ")"
		}
	default:
		s += ", start time not logged (the iPod's clock was not set)"
	}
	if !b.Start {
		s += "; its first lines have been overwritten by the ring"
	}
	return s
}

func endSentence(end string) string {
	switch end {
	case "disk mode":
		return "went into disk mode"
	case "sleep":
		return "went to sleep"
	case "standby":
		return "switched off (standby)"
	case "low battery":
		return "stopped writing on a low battery"
	case "battery empty":
		return "switched off on an empty battery"
	}
	return end
}

func imageTail(img ImageFacts) string {
	if img.Known && img.Version != "" {
		return "; the iPod holds Core " + img.Version
	}
	return ""
}

// versionWords names a version the way the lines say it.
func versionWords(v string) string {
	if v == "" {
		return "an image with no version marker"
	}
	return v
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "an unknown date"
	}
	return t.UTC().Format("2006-01-02 15:04") + " UTC"
}

// ago is a duration the way a person says it.
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute") + " ago"
	case d < 48*time.Hour:
		return plural(int(d/time.Hour), "hour") + " ago"
	default:
		return plural(int(d/(24*time.Hour)), "day") + " ago"
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

// CheckBoot reads the volume's log and flash record and evaluates them
// against the device half's image and the volume half's index.
func CheckBoot(volume string, dev DeviceReport, vol VolumeReport, now time.Time) BootReport {
	if volume == "" {
		r := &report{}
		r.line(Skip, "last boot", "no mounted volume, so no log to read")
		return BootReport{Checks: r.checks}
	}
	in := BootInputs{
		Image:   dev.Image,
		IndexOK: vol.IndexOK,
		Songs:   vol.Songs,
		Now:     now,
	}
	in.Log, in.LogErr = os.ReadFile(filepath.Join(volume, devicefs.LogName))
	in.Record, in.RecordFound, in.RecordErr = devicefs.ReadFlashRecord(volume)
	return EvaluateBoot(in)
}
