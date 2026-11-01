// SPDX-License-Identifier: Apache-2.0

package devicefs

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// COREFLSH.TXT — the host's note of the last firmware write, kept on the music
// volume so `core doctor --boot` can tell whether the iPod has started that
// image since.
//
// WHY A NOTE AT ALL. The firmware's event log (CORELOG.BIN) says when the iPod
// booted — every block carries a boot id — but not WHICH build booted: the
// version marker is in the image for the host to find, and nothing prints it.
// What the host can know is what it wrote and where the log stood at that
// moment. A boot id in the log newer than the one noted here is a boot that
// happened after the write, and so a boot of the written image.
//
// It lives on the FAT volume, not in the user's config directory, for two
// reasons: on Windows the write runs in an elevated child that may be a
// different account with a different config directory, and the note should
// travel with the iPod to whichever computer asks next.
//
// The firmware never opens it. It is plain "key value" lines so that a person
// who finds it on their iPod can read it.
const FlashRecordName = "COREFLSH.TXT"

// FlashRecord is what one firmware write leaves behind.
type FlashRecord struct {
	// Version and BuildID are the written image's CORE-FW-VERSION marker;
	// both empty for an image without one.
	Version, BuildID string
	// Length and Checksum are the OSOS directory row as written: what ties
	// this note to the image that is on the iPod now.
	Length, Checksum uint32
	// Replaced describes what the write replaced, and ReplacedVersion is
	// its version when it was a Core image with a marker.
	Replaced, ReplacedVersion string
	// Flashed is when the write was verified.
	Flashed time.Time
	// LogKnown says CORELOG.BIN was valid when the write happened; LogID
	// is its file id and LogBoot the boot id of its newest block (0 for an
	// empty ring). A later boot of the device shows up as a newer boot id
	// in the same file, or as a different file id (the log was recreated
	// after the write, so anything in it is newer than the write).
	LogKnown bool
	LogID    uint32
	LogBoot  uint16
}

// LogStateOf reads the two numbers a FlashRecord keeps about a log image.
// ok is false when the image has no valid header.
func LogStateOf(logData []byte) (fileID uint32, boot uint16, ok bool) {
	r, err := ReadLogRing(logData)
	if err != nil {
		return 0, 0, false
	}
	if b, have := r.Newest(); have {
		boot = b.Boot
	}
	return r.FileID, boot, true
}

// Encode renders the note. The order is fixed so the file diffs cleanly.
func (r FlashRecord) Encode() []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Written by core after it flashed this iPod. `core doctor --boot` reads it.\n")
	fmt.Fprintf(&b, "# The iPod never reads it; deleting it only turns that check off.\n")
	kv := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s %s\n", k, v)
		}
	}
	kv("version", r.Version)
	kv("build", r.BuildID)
	kv("length", strconv.FormatUint(uint64(r.Length), 10))
	kv("checksum", fmt.Sprintf("%#08x", r.Checksum))
	kv("replaced", oneLine(r.Replaced))
	kv("replaced-version", r.ReplacedVersion)
	if !r.Flashed.IsZero() {
		kv("flashed", r.Flashed.UTC().Format(time.RFC3339))
	}
	if r.LogKnown {
		kv("log-id", fmt.Sprintf("%#08x", r.LogID))
		kv("log-boot", strconv.FormatUint(uint64(r.LogBoot), 10))
	} else {
		kv("log-id", "none")
	}
	return b.Bytes()
}

func oneLine(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(s))
}

// ErrFlashRecord is a note that does not parse.
var ErrFlashRecord = errors.New("devicefs: " + FlashRecordName + " does not parse")

// ParseFlashRecord reads a note back. Unknown keys are ignored (a newer core
// may add some); the three that make it useful — length, checksum and the log
// state — are required.
func ParseFlashRecord(b []byte) (FlashRecord, error) {
	var r FlashRecord
	have := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, _ := strings.Cut(line, " ")
		v = strings.TrimSpace(v)
		var err error
		switch k {
		case "version":
			r.Version = v
		case "build":
			r.BuildID = v
		case "length":
			var n uint64
			n, err = strconv.ParseUint(v, 10, 32)
			r.Length = uint32(n)
		case "checksum":
			var n uint64
			n, err = strconv.ParseUint(v, 0, 32)
			r.Checksum = uint32(n)
		case "replaced":
			r.Replaced = v
		case "replaced-version":
			r.ReplacedVersion = v
		case "flashed":
			r.Flashed, err = time.Parse(time.RFC3339, v)
		case "log-id":
			if v != "none" {
				var n uint64
				n, err = strconv.ParseUint(v, 0, 32)
				r.LogID, r.LogKnown = uint32(n), true
			}
		case "log-boot":
			var n uint64
			n, err = strconv.ParseUint(v, 10, 16)
			r.LogBoot = uint16(n)
		default:
			continue
		}
		if err != nil {
			return FlashRecord{}, fmt.Errorf("%w: %s: %v", ErrFlashRecord, k, err)
		}
		have[k] = true
	}
	for _, k := range []string{"length", "checksum", "log-id"} {
		if !have[k] {
			return FlashRecord{}, fmt.Errorf("%w: no %s line", ErrFlashRecord, k)
		}
	}
	if r.LogKnown && !have["log-boot"] {
		return FlashRecord{}, fmt.Errorf("%w: no log-boot line", ErrFlashRecord)
	}
	return r, nil
}

// WriteFlashRecord writes the note into volumeRoot, replacing any earlier one.
func WriteFlashRecord(volumeRoot string, r FlashRecord) (string, error) {
	if err := RefusesMount(volumeRoot); err != nil {
		return "", err
	}
	path := filepath.Join(volumeRoot, FlashRecordName)
	return path, writeFileThrough(path, r.Encode())
}

// ReadFlashRecord reads volumeRoot's note. found is false, with no error, when
// there is none.
func ReadFlashRecord(volumeRoot string) (r FlashRecord, found bool, err error) {
	b, err := os.ReadFile(filepath.Join(volumeRoot, FlashRecordName))
	if errors.Is(err, os.ErrNotExist) {
		return FlashRecord{}, false, nil
	}
	if err != nil {
		return FlashRecord{}, true, err
	}
	r, err = ParseFlashRecord(b)
	return r, true, err
}
