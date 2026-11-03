// SPDX-License-Identifier: Apache-2.0

package devicefs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// dumpLike renders entries the way make_log.py's render_dump prints them,
// line for line, so the parity tests below can compare what the window
// shows (the order, the boot boundaries, the gaps, the text) against the
// firmware's own reader.
func dumpLike(r LogRing) string {
	ents := OrderedLogEntries(r)
	var b strings.Builder
	fmt.Fprintf(&b, "# %s: %d blocks, id %08X, %d written\n", LogName, r.Blocks, r.FileID, len(ents))
	if len(ents) == 0 {
		b.WriteString("# (empty: nothing has been flushed yet)\n")
		return b.String()
	}
	for i, e := range ents {
		if e.Missing != 0 {
			fmt.Fprintf(&b, "# --- %d block(s) missing (overwritten by the ring, or torn) ---\n", e.Missing)
		}
		if i == 0 || e.Boot != ents[i-1].Boot {
			fmt.Fprintf(&b, "# === boot %d ===\n", e.Boot)
		}
		b.WriteString(e.Text)
		if e.Text != "" && !strings.HasSuffix(e.Text, "\n") {
			b.WriteString("\n")
		}
		if e.Final {
			fmt.Fprintf(&b, "# --- final flush (seq %d) ---\n", e.Seq)
		}
	}
	return b.String()
}

// makeLogDump runs the firmware's reader over a file: make_log.py --dump.
// -X utf8 pins stdout's encoding, so a U+FFFD prints the same on every
// machine.
func makeLogDump(t *testing.T, repo, file string) string {
	t.Helper()
	py := python3(t)
	out, err := exec.Command(py, "-I", "-X", "utf8", filepath.Join(repo, "tools", "make_log.py"),
		"--dump", file).Output()
	if err != nil {
		t.Fatalf("make_log.py --dump: %v", err)
	}
	s := string(out)
	if runtime.GOOS == "windows" {
		s = strings.ReplaceAll(s, "\r\n", "\n") // text-mode stdout
	}
	return s
}

func compareLines(t *testing.T, mine, theirs string) {
	t.Helper()
	a, b := strings.Split(mine, "\n"), strings.Split(theirs, "\n")
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y string
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			t.Errorf("line %d:\n  Go:          %q\n  make_log.py: %q", i+1, x, y)
		}
	}
}

// awkwardRing is a ring that exercises every rule the dump has: three boots,
// slots out of seq order after a wrap, a torn block (a gap of one) and an
// overwritten stretch (a gap of several), CRLF, a block with no trailing
// newline, a block cut in the middle of a UTF-8 sequence, a stray byte, and
// two forced flushes.
func awkwardRing(t *testing.T) []byte {
	t.Helper()
	const count = 10 // header + nine slots
	type ent struct {
		seq   uint32
		boot  uint16
		text  string
		final bool
	}
	ents := []ent{
		{11, 4, "core: kernel alive\r\ncore: version v0.2.1 build v0.2.1\r\n", false},
		{12, 4, "core: suspend: entering\n", true},
		{13, 5, "core: kernel alive\ncore: library songs 0000038B albums 00000065 ms 00000ABC\n", false},
		{14, 5, "play: Sigur R\xc3\xb3s - \xc3", false}, // cut mid-é, no newline
		// 15 is torn: written below with a bad CRC.
		{16, 5, "core: standby: entering\n", true},
		{17, 6, "core: kernel alive\ncore: version v0.2.2 build v0.2.2-5-g39fdaf9\n", false},
		{18, 6, "stray \xff byte, surrogate \xed\xa0\x80, cut \xe2\x82 euro\n", false},
	}
	data := make([]byte, count*LogBlockBytes)
	h := EncodeLogHeader(count, 0x00C0FFEE)
	copy(data, h[:])
	put := func(seq uint32, blk [LogBlockBytes]byte) {
		copy(data[int(LogBlockIndex(seq, count))*LogBlockBytes:], blk[:])
	}
	for _, e := range ents {
		b, err := EncodeLogBlock(e.seq, e.boot, []byte(e.text), e.final)
		if err != nil {
			t.Fatal(err)
		}
		put(e.seq, b)
	}
	torn, _ := EncodeLogBlock(15, 5, []byte("core: half a block\n"), false)
	torn[LogBlockHdr] ^= 0x20
	put(15, torn)
	// The one slot 11..18 left alone still holds seq 1, from before the
	// wrap: an overwritten stretch of 2..10 between it and seq 11.
	old, _ := EncodeLogBlock(1, 1, []byte("core: boot 1, long ago\n"), false)
	put(1, old)
	return data
}

// The parity the window's iPod tab rests on: for the fixture the firmware's
// tool emits, and for a ring with every awkward case in it, the Go order and
// text are make_log.py --dump's, line for line.
func TestOrderedLogEntriesMatchMakeLogDump(t *testing.T) {
	repo := repoRoot(t)
	if repo == "" {
		return
	}
	py := python3(t)
	if py == "" {
		return
	}
	dir := t.TempDir()

	emitted := filepath.Join(dir, "emitted.bin")
	if b, err := exec.Command(py, "-I", filepath.Join(repo, "tools", "make_log.py"), "--emit", emitted).
		CombinedOutput(); err != nil {
		t.Fatalf("make_log.py --emit: %v\n%s", err, b)
	}
	awkward := filepath.Join(dir, "awkward.bin")
	if err := os.WriteFile(awkward, awkwardRing(t), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{emitted, awkward} {
		t.Run(filepath.Base(f), func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			r, err := ReadLogRing(data)
			if err != nil {
				t.Fatal(err)
			}
			compareLines(t, dumpLike(r), makeLogDump(t, repo, f))
		})
	}
}

// The same facts with no firmware checkout: the awkward ring's order, gaps
// and text, pinned by hand.
func TestOrderedLogEntriesAwkwardRing(t *testing.T) {
	r, err := ReadLogRing(awkwardRing(t))
	if err != nil {
		t.Fatal(err)
	}
	got := OrderedLogEntries(r)
	want := []struct {
		seq     uint32
		boot    uint16
		missing int64
		final   bool
		text    string
	}{
		{1, 1, 0, false, "core: boot 1, long ago\n"},
		{11, 4, 9, false, "core: kernel alive\ncore: version v0.2.1 build v0.2.1\n"},
		{12, 4, 0, true, "core: suspend: entering\n"},
		{13, 5, 0, false, "core: kernel alive\ncore: library songs 0000038B albums 00000065 ms 00000ABC\n"},
		{14, 5, 0, false, "play: Sigur Rós - \uFFFD"},
		{16, 5, 1, true, "core: standby: entering\n"},
		{17, 6, 0, false, "core: kernel alive\ncore: version v0.2.2 build v0.2.2-5-g39fdaf9\n"},
		{18, 6, 0, false, "stray \uFFFD byte, surrogate \uFFFD\uFFFD\uFFFD, cut \uFFFD euro\n"},
	}
	if len(got) != len(want) {
		t.Fatalf("%d entries, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Seq != w.seq || g.Boot != w.boot || g.Missing != w.missing || g.Final != w.final || g.Text != w.text {
			t.Errorf("entry %d = %+v, want %+v", i, g, w)
		}
	}
}

// LogText is Python's decode("utf-8", "replace"): one U+FFFD per maximal
// subpart. Each expectation was printed by CPython.
func TestLogTextReplacesLikePython(t *testing.T) {
	for in, want := range map[string]string{
		"a\xe2\x82b":           "a\uFFFDb",                 // cut 3-byte: ONE
		"\xed\xa0\x80":         "\uFFFD\uFFFD\uFFFD",       // surrogate: three
		"\xf0\x9f\x98":         "\uFFFD",                   // cut 4-byte: one
		"x\r\ny\r":             "x\ny\r",                   // CRLF only
		"\xc3\xa9\xff\xc0\xaf": "é\uFFFD\uFFFD\uFFFD",      // 0xFF, overlong C0
		"\xf4\x90\x80\x80":     "\uFFFD\uFFFD\uFFFD\uFFFD", // above U+10FFFF
		"\xe0\x80\xaf":         "\uFFFD\uFFFD\uFFFD",       // overlong E0
		"caf\xc3":              "caf\uFFFD",                // cut at the end
		"plain\n":              "plain\n",
	} {
		if got := LogText([]byte(in)); got != want {
			t.Errorf("LogText(%q) = %q, want %q", in, got, want)
		}
	}
}
