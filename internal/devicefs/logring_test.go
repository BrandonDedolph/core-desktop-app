// SPDX-License-Identifier: Apache-2.0

package devicefs

import (
	"bytes"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// makeLogFixture is tools/make_log.py's FIXTURE_ENTRIES, rebuilt here: a
// six-block file (header + five ring slots) after nine writes, so seqs 0..3
// have been overwritten by 5..8, boot 2 starts at seq 6 and seq 5 was a
// forced flush.
var makeLogFixture = []struct {
	seq   uint32
	boot  uint16
	text  string
	final bool
}{
	{0, 1, "core: boot 1 first\n", false},
	{1, 1, "core: boot 1 second\n", false},
	{2, 1, "core: boot 1 third\n", false},
	{3, 1, "core: boot 1 fourth\n", false},
	{4, 1, "core: boot 1 fifth\n", false},
	{5, 1, "core: suspend: entering\n", true},
	{6, 2, "core: boot 2 first\n", false},
	{7, 2, "core: boot 2 second\n", false},
	{8, 2, "core: boot 2 third\n", false},
}

func synthMakeLogFixture(t *testing.T) []byte {
	t.Helper()
	const count = 6
	data := make([]byte, count*LogBlockBytes)
	h := EncodeLogHeader(count, logGoldenFileID)
	copy(data, h[:])
	for _, e := range makeLogFixture {
		b, err := EncodeLogBlock(e.seq, e.boot, []byte(e.text), e.final)
		if err != nil {
			t.Fatal(err)
		}
		copy(data[int(LogBlockIndex(e.seq, count))*LogBlockBytes:], b[:])
	}
	return data
}

// checkMakeLogFixture asserts what make_log.py's --selftest asserts of the
// same ring: blocks come back in seq order whatever slot they sit in, the
// overwritten ones are gone, the boot boundary and the final flag survive.
func checkMakeLogFixture(t *testing.T, data []byte) {
	t.Helper()
	r, err := ReadLogRing(data)
	if err != nil {
		t.Fatal(err)
	}
	if r.Blocks != 6 || r.FileID != logGoldenFileID {
		t.Errorf("header: %d blocks, id %#x", r.Blocks, r.FileID)
	}
	if len(r.Entries) != 5 {
		t.Fatalf("%d entries, want 5", len(r.Entries))
	}
	for i, e := range r.Entries {
		want := makeLogFixture[4+i]
		if e.Seq != want.seq || e.Boot != want.boot || e.Final != want.final ||
			string(e.Text) != want.text {
			t.Errorf("entry %d = {%d %d %v %q}, want %+v", i, e.Seq, e.Boot, e.Final, e.Text, want)
		}
	}
	if n, _ := r.Newest(); n.Seq != 8 || n.Boot != 2 {
		t.Errorf("newest = seq %d boot %d", n.Seq, n.Boot)
	}
}

func TestReadLogRingMakeLogFixture(t *testing.T) {
	checkMakeLogFixture(t, synthMakeLogFixture(t))
}

// A block one byte off is not a block: magic, length and CRC all refuse.
func TestDecodeLogBlockRefuses(t *testing.T) {
	good, err := EncodeLogBlock(3, 7, []byte("core: kernel alive\n"), true)
	if err != nil {
		t.Fatal(err)
	}
	if b, ok := DecodeLogBlock(good[:]); !ok || b.Seq != 3 || b.Boot != 7 || !b.Final {
		t.Fatalf("good block: %+v %v", b, ok)
	}
	mut := func(f func(b []byte)) []byte {
		c := append([]byte(nil), good[:]...)
		f(c)
		return c
	}
	for name, b := range map[string][]byte{
		"zero (unwritten)": make([]byte, LogBlockBytes),
		"magic":            mut(func(b []byte) { b[0] ^= 1 }),
		"text torn":        mut(func(b []byte) { b[LogBlockHdr+2] ^= 0x20 }),
		"tail torn":        mut(func(b []byte) { b[LogBlockBytes-1] = 1 }),
		"length too long":  mut(func(b []byte) { b[logBlkOffLen] = 0xFF; b[logBlkOffLen+1] = 0x7F }),
		"short":            good[:LogBlockBytes-1],
	} {
		if _, ok := DecodeLogBlock(b); ok {
			t.Errorf("%s: decoded", name)
		}
	}
	if _, err := EncodeLogBlock(0, 0, make([]byte, LogBlockText+1), false); err == nil {
		t.Error("an over-long text encoded")
	}
}

func TestReadLogRingBadHeader(t *testing.T) {
	if _, err := ReadLogRing(make([]byte, 4*LogBlockBytes)); err != ErrLogHeader {
		t.Errorf("err = %v", err)
	}
	// A file cut short keeps the blocks it still has.
	data := synthMakeLogFixture(t)
	r, err := ReadLogRing(data[:3*LogBlockBytes])
	if err != nil || len(r.Entries) != 2 {
		t.Errorf("truncated: %d entries, %v", len(r.Entries), err)
	}
}

func TestLogStateOf(t *testing.T) {
	id, boot, ok := LogStateOf(synthMakeLogFixture(t))
	if !ok || id != logGoldenFileID || boot != 2 {
		t.Errorf("got %#x %d %v", id, boot, ok)
	}
	h := EncodeLogHeader(4, 9)
	empty := append(h[:], make([]byte, 3*LogBlockBytes)...)
	if id, boot, ok := LogStateOf(empty); !ok || id != 9 || boot != 0 {
		t.Errorf("empty ring: %#x %d %v", id, boot, ok)
	}
	if _, _, ok := LogStateOf(nil); ok {
		t.Error("no file read as a log")
	}
}

// The reference implementation, re-run: make_log.py --emit writes the
// fixture core/tests/kernel/evlog_test.c decodes with the FIRMWARE's
// decoder. The same bytes must decode here, and this side's encoder must
// produce them.
func TestReadLogRingMatchesMakeLogEmit(t *testing.T) {
	repo := repoRoot(t)
	if repo == "" {
		t.Skip("firmware repo not given (set CORE_REPO=/path/to/core-firmware)")
	}
	py := python3(t)
	if py == "" {
		t.Skip("python3 not on PATH")
	}
	out := filepath.Join(t.TempDir(), "CORELOG.BIN")
	cmd := exec.Command(py, "-I", filepath.Join(repo, "tools", "make_log.py"), "--emit", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make_log.py --emit: %v\n%s", err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	checkMakeLogFixture(t, data)
	if mine := synthMakeLogFixture(t); !bytes.Equal(mine, data) {
		t.Errorf("EncodeLogBlock differs from make_log.py: %s",
			firstDiff(hex.EncodeToString(mine), hex.EncodeToString(data)))
	}
}
