package id3

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hajimehoshi/go-mp3"
)

// audioFrames is n real MPEG-1 Layer III frames (128 kbps, 44.1 kHz,
// stereo): a header, all-zero side information — part2_3_length 0, so every
// granule decodes to silence — and a main-data area filled with a pattern,
// so a writer that moved, dropped or zeroed one byte of the audio changes
// its hash. go-mp3 and ffmpeg both decode it.
func audioFrames(n int) []byte {
	out := make([]byte, 0, n*417)
	x := uint32(0x9e3779b9)
	for i := 0; i < n; i++ {
		f := make([]byte, 417)
		f[0], f[1], f[2], f[3] = 0xff, 0xfb, 0x90, 0x00
		for j := 4 + 32; j < len(f); j++ {
			x = x*1664525 + 1013904223
			f[j] = byte(x >> 24 & 0x7f) // never 0xff: no false sync words
		}
		out = append(out, f...)
	}
	return out
}

// v1Tail is an ID3v1 tag, which is part of "everything after the head tag".
func v1Tail() []byte {
	t := make([]byte, 128)
	copy(t, "TAGOld v1 title")
	return t
}

// tinyPNG is a real 4×4 PNG, so ffmpeg can decode the cover as well.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 13)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func txxx(desc, value string) []byte {
	return append(append([]byte{0}, desc+"\x00"...), value...)
}

func apic(data []byte) []byte {
	b := append([]byte{0}, "image/png\x00"...)
	b = append(b, 3, 0)
	return append(b, data...)
}

// tagged builds a file: an ID3v2 tag of `major` with a ripper's frames, pad
// bytes of padding, the audio, and an ID3v1 tail.
func tagged(t *testing.T, major byte, pad int) (path string, tagLen int) {
	t.Helper()
	b := newTag(major, 0).
		ascii("TIT2", "Psycho Killer").
		ascii("TPE1", "Talking Heads").
		frame("APIC", apic(tinyPNG(t)), 0).
		ascii("TALB", "Talking Heads: 77").
		frame("TXXX", txxx("REPLAYGAIN_TRACK_GAIN", "-6.50 dB"), 0).
		frame("TXXX", txxx("REPLAYGAIN_TRACK_PEAK", "0.977"), 0).
		frame("COMM", append([]byte{0}, "eng\x00ripped by EAC"...), 0).
		ascii("TRCK", "11/11").
		ascii("TYER", "1977")
	if pad > 0 {
		b.raw(make([]byte, pad))
	}
	head := b.done(0)
	file := append(append(append([]byte{}, head...), audioFrames(30)...), v1Tail()...)
	path = filepath.Join(t.TempDir(), "track.mp3")
	if err := os.WriteFile(path, file, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, len(head)
}

// rest is everything after the head tag, hashed.
func rest(t *testing.T, path string) (sum [32]byte, tagEnd int) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := splitTag(b)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b[tag.end:]), tag.end
}

// frameList is the tag's frames as "ID:raw body", in order.
func frameList(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := splitTag(b)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range tag.frames {
		out = append(out, string(f[:4])+":"+string(f[10:]))
	}
	return out
}

func readPath(t *testing.T, path string) *Meta {
	t.Helper()
	m, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestWriteTagsInPlace: the tag has room, so the file keeps its size, not a
// byte after the tag changes, and the frames that were not edited — the
// cover, ReplayGain, the comment — keep their bytes and their order.
func TestWriteTagsInPlace(t *testing.T) {
	for _, major := range []byte{3, 4} {
		path, tagLen := tagged(t, major, 1024)
		before, _ := os.ReadFile(path)
		audio, _ := rest(t, path)
		old := frameList(t, path)

		if err := WriteTags(path, map[string]string{"title": "Psycho Killer (live)", "album_artist": "Talking Heads"}); err != nil {
			t.Fatalf("v2.%d: %v", major, err)
		}
		after, _ := os.ReadFile(path)
		if len(after) != len(before) {
			t.Errorf("v2.%d: size %d -> %d", major, len(before), len(after))
		}
		if !bytes.Equal(after[tagLen:], before[tagLen:]) {
			t.Errorf("v2.%d: a byte after the tag changed", major)
		}
		if a, end := rest(t, path); a != audio || end != tagLen {
			t.Errorf("v2.%d: the audio moved or changed (tag end %d, was %d)", major, end, tagLen)
		}
		now := frameList(t, path)
		if len(now) != len(old)+1 {
			t.Fatalf("v2.%d: frames %q", major, now)
		}
		for i := 1; i < len(old); i++ {
			if now[i] != old[i] {
				t.Errorf("v2.%d: frame %d changed: %q -> %q", major, i, old[i], now[i])
			}
		}
		if !strings.HasPrefix(now[0], "TIT2:") || !strings.HasPrefix(now[len(now)-1], "TPE2:") {
			t.Errorf("v2.%d: the title is not where it was, or the new frame is not last: %q", major, now)
		}
		m := readPath(t, path)
		if m.Tags["title"] != "Psycho Killer (live)" || m.Tags["album_artist"] != "Talking Heads" ||
			m.Tags["artist"] != "Talking Heads" || m.Tags["track"] != "11/11" || m.FrontCover() == nil {
			t.Errorf("v2.%d: read back %v", major, m.Tags)
		}
		if m.DurationSeconds() != readPath(t, path).DurationSeconds() || m.SampleRate != 44100 {
			t.Errorf("v2.%d: the stream reads differently", major)
		}
	}
}

// TestWriteTagsGrowsATagWithNoPadding: no room, so the file is rewritten
// with the new tag and DefaultPadding in front of the old file's tail —
// which is byte for byte what followed the old tag.
func TestWriteTagsGrowsATagWithNoPadding(t *testing.T) {
	path, tagLen := tagged(t, 3, 0)
	audio, _ := rest(t, path)
	before, _ := os.ReadFile(path)
	if err := WriteTags(path, map[string]string{"genre": "New Wave"}); err != nil {
		t.Fatal(err)
	}
	a, end := rest(t, path)
	if a != audio {
		t.Error("the audio changed in the rewrite")
	}
	wantEnd := tagLen + 10 + 1 + len("New Wave") + DefaultPadding
	if end != wantEnd {
		t.Errorf("tag end %d, want %d", end, wantEnd)
	}
	after, _ := os.ReadFile(path)
	if len(after) != len(before)+(wantEnd-tagLen) {
		t.Errorf("size %d -> %d", len(before), len(after))
	}
	if m := readPath(t, path); m.Tags["genre"] != "New Wave" || m.Tags["title"] != "Psycho Killer" {
		t.Errorf("read back %v", m.Tags)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*core-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
	// The next edit fits.
	size := len(after)
	if err := WriteTags(path, map[string]string{"genre": "Punk"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); len(b) != size {
		t.Error("the second edit did not land in the padding")
	}
}

// A file with no ID3v2 tag gets a v2.3 one in front; the whole old file
// follows it unchanged.
func TestWriteTagsOnAnUntaggedFile(t *testing.T) {
	old := append(audioFrames(20), v1Tail()...)
	path := filepath.Join(t.TempDir(), "bare.mp3")
	if err := os.WriteFile(path, old, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteTags(path, map[string]string{"title": "Untitled", "track": "1"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if b[3] != 3 {
		t.Errorf("major %d, want 3", b[3])
	}
	_, end := rest(t, path)
	if !bytes.Equal(b[end:], old) {
		t.Error("the old file is not what follows the new tag")
	}
	if m := readPath(t, path); m.Tags["title"] != "Untitled" || m.Tags["track"] != "1" {
		t.Errorf("read back %v", m.Tags)
	}
}

// Text that ISO-8859-1 cannot hold is UTF-16 in v2.3 and UTF-8 in v2.4; the
// date is TYER in v2.3 and TDRC in v2.4, and setting it takes the other
// spelling out; an empty value removes the key.
func TestWriteTagsEncodingsDatesAndRemoval(t *testing.T) {
	for _, major := range []byte{3, 4} {
		path, _ := tagged(t, major, 512)
		title := "Ça plane pour moi — 🎸"
		if err := WriteTags(path, map[string]string{"title": title, "date": "1978", "track": ""}); err != nil {
			t.Fatal(err)
		}
		m := readPath(t, path)
		if m.Tags["title"] != title || m.Tags["date"] != "1978" {
			t.Errorf("v2.%d: %v", major, m.Tags)
		}
		if _, ok := m.Tags["track"]; ok {
			t.Errorf("v2.%d: the track survived its removal", major)
		}
		frames := strings.Join(frameList(t, path), "|")
		enc := byte(3)
		date, other := "TDRC", "TYER"
		if major == 3 {
			enc, date, other = 1, "TYER", "TDRC"
		}
		if !strings.Contains(frames, "TIT2:"+string([]byte{enc})) {
			t.Errorf("v2.%d: title not in encoding %d", major, enc)
		}
		if !strings.Contains(frames, date+":") || strings.Contains(frames, other+":") || strings.Contains(frames, "TRCK:") {
			t.Errorf("v2.%d: frames %q", major, frames)
		}
	}
}

// Refused, untouched: a tag cut short, an ID3v2.2 tag, an unsynchronised
// tag, a file with no audio after its tag, an unknown key, a frame that
// claims more bytes than the tag has.
func TestWriteTagsRefusals(t *testing.T) {
	good, _ := tagged(t, 3, 256)
	whole, _ := os.ReadFile(good)
	_, end := rest(t, good)

	broken := append([]byte{}, whole...)
	broken[10+4] = 0x7f // TIT2's size, now far past the tag

	cases := map[string]struct {
		data []byte
		set  map[string]string
	}{
		"cut inside the tag": {whole[:end-100], map[string]string{"title": "x"}},
		"cut to the header":  {whole[:8], map[string]string{"title": "x"}},
		"v2.2":               {newTag(2, 0).ascii("TT2", "x").done(20), map[string]string{"title": "x"}},
		"unsynchronised":     {newTag(3, 0x80).ascii("TIT2", "x").done(20), map[string]string{"title": "x"}},
		"no audio":           {newTag(3, 0).ascii("TIT2", "x").raw(make([]byte, 64)).done(0), map[string]string{"title": "x"}},
		"unknown key":        {whole, map[string]string{"lyrics": "x"}},
		"frame overruns":     {broken, map[string]string{"title": "x"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.mp3")
			if err := os.WriteFile(path, tc.data, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := WriteTags(path, tc.set); err == nil {
				t.Fatal("accepted")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(after, tc.data) {
				t.Errorf("modified: %d -> %d bytes", len(tc.data), len(after))
			}
			if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*core-*")); len(left) != 0 {
				t.Errorf("temporary files left: %v", left)
			}
		})
	}
}

// A crash between the temporary file and the rename leaves the original
// whole.
func TestWriteTagsRewriteSurvivesACrash(t *testing.T) {
	path, _ := tagged(t, 3, 0)
	before, _ := os.ReadFile(path)
	testHookBeforeRename = func(string) error { return errors.New("power cut") }
	defer func() { testHookBeforeRename = nil }()
	if err := WriteTags(path, map[string]string{"genre": "Rock"}); err == nil {
		t.Fatal("no error")
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, before) {
		t.Error("the original changed")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*core-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

// decode is the file's PCM through go-mp3, the decoder the app plays with.
func decode(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := mp3.NewDecoder(f)
	if err != nil {
		t.Fatalf("go-mp3: %v", err)
	}
	pcm, err := io.ReadAll(d)
	if err != nil {
		t.Fatalf("go-mp3: %v", err)
	}
	return pcm
}

// The decoders' opinion: go-mp3 gives the same PCM before and after, both
// paths; ffprobe (when installed) reads the new title and the old
// ReplayGain, and ffmpeg decodes with nothing to say.
func TestWriteTagsDecodersAgree(t *testing.T) {
	for _, pad := range []int{1024, 0} {
		path, _ := tagged(t, 4, pad)
		pcm := decode(t, path)
		if len(pcm) == 0 {
			t.Fatal("no PCM")
		}
		if err := WriteTags(path, map[string]string{"title": "Pulled Up", "album": "77"}); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(decode(t, path), pcm) {
			t.Errorf("pad %d: the decoded audio changed", pad)
		}
		ffprobe, ffmpeg := lookTool("ffprobe"), lookTool("ffmpeg")
		if ffprobe == "" || ffmpeg == "" {
			continue
		}
		out, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "format_tags", "-of", "default=nw=1", path).Output()
		if err != nil {
			t.Fatal(err)
		}
		tags := string(out)
		for _, want := range []string{"TAG:title=Pulled Up", "TAG:album=77", "TAG:REPLAYGAIN_TRACK_GAIN=-6.50 dB", "TAG:artist=Talking Heads"} {
			if !strings.Contains(tags, want) {
				t.Errorf("pad %d: ffprobe lacks %q:\n%s", pad, want, tags)
			}
		}
		var stderr bytes.Buffer
		cmd := exec.Command(ffmpeg, "-v", "error", "-i", path, "-f", "null", "-")
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil || stderr.Len() != 0 {
			t.Errorf("pad %d: ffmpeg: %v %s", pad, err, stderr.String())
		}
	}
}
