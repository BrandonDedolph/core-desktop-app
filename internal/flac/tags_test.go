package flac

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// taggedFixture is a FLAC file the way a ripper leaves one: a comment block
// whose keys are in mixed case and in no particular order, a repeated
// ARTIST, ReplayGain and MusicBrainz ids, a front cover, and padding bytes
// of PADDING (0 = none, so the tag writer has to rewrite the file).
func taggedFixture(t *testing.T, padding int, comments ...string) string {
	t.Helper()
	if len(comments) == 0 {
		comments = []string{
			"TITLE=Once in a Lifetime",
			"Artist=Talking Heads",
			"ARTIST=Brian Eno",
			"ALBUM=Remain in Light",
			"REPLAYGAIN_TRACK_GAIN=-7.32 dB",
			"REPLAYGAIN_TRACK_PEAK=0.988403",
			"MUSICBRAINZ_TRACKID=4f8a2c1e-3b0a-4c6e-9d8f-0a1b2c3d4e5f",
			"tracknumber=4",
			"DATE=1980",
		}
	}
	data := BuildFileP(testInfo, nil, []Picture{{Type: PictureTypeFrontCover, MIME: "image/png",
		Width: 8, Height: 8, Depth: 32, Data: testPNG(t, 8, 8)}}, padding)
	path := writeTemp(t, "tagged.flac", data)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c, err := readChain(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	body := appendU32LE(nil, uint32(len("reference libFLAC 1.4.3 20230623")))
	body = append(body, "reference libFLAC 1.4.3 20230623"...)
	body = appendU32LE(body, uint32(len(comments)))
	for _, cm := range comments {
		body = appendU32LE(body, uint32(len(cm)))
		body = append(body, cm...)
	}
	for i := range c.blocks {
		if c.blocks[i].typ == blockVorbisComment {
			c.blocks[i].body = body
		}
	}
	out := append([]byte("fLaC"), encodeChain(c.blocks)...)
	out = append(out, data[c.audioOff:]...)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// rawComments is the comment list exactly as it sits in the file.
func rawComments(t *testing.T, path string) (vendor string, list []string) {
	t.Helper()
	for _, b := range chainOf(t, path).blocks {
		if b.typ != blockVorbisComment {
			continue
		}
		vc, err := splitComments(b.body)
		if err != nil {
			t.Fatal(err)
		}
		vendor = string(vc.vendor)
		for _, c := range vc.comments {
			list = append(list, string(c))
		}
	}
	return vendor, list
}

func pictureBodies(t *testing.T, path string) [][]byte {
	t.Helper()
	var out [][]byte
	for _, b := range chainOf(t, path).blocks {
		if b.typ == blockPicture {
			out = append(out, b.body)
		}
	}
	return out
}

// TestWriteTagsInPlace is the padding path: the file keeps its size, the
// audio its bytes, and the comment list changes in exactly the two places
// that were edited — the other comments keep their spelling, their order
// and their bytes, ReplayGain and the cover included.
func TestWriteTagsInPlace(t *testing.T) {
	path := taggedFixture(t, 4096)
	size, audio := fileSize(t, path), audioDigest(t, path)
	pics := pictureBodies(t, path)
	vendor, _ := rawComments(t, path)

	if err := WriteTags(path, map[string]string{"title": "Once in a Lifetime (remastered)", "Album": "Remain In Light"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := fileSize(t, path); got != size {
		t.Errorf("size %d -> %d: the edit had room and still moved the audio", size, got)
	}
	if got := audioDigest(t, path); got != audio {
		t.Error("audio frames changed")
	}
	if got := pictureBodies(t, path); len(got) != 1 || !bytes.Equal(got[0], pics[0]) {
		t.Error("the cover changed")
	}
	v, list := rawComments(t, path)
	if v != vendor {
		t.Errorf("vendor %q -> %q", vendor, v)
	}
	want := []string{
		"TITLE=Once in a Lifetime (remastered)",
		"Artist=Talking Heads",
		"ARTIST=Brian Eno",
		"ALBUM=Remain In Light",
		"REPLAYGAIN_TRACK_GAIN=-7.32 dB",
		"REPLAYGAIN_TRACK_PEAK=0.988403",
		"MUSICBRAINZ_TRACKID=4f8a2c1e-3b0a-4c6e-9d8f-0a1b2c3d4e5f",
		"tracknumber=4",
		"DATE=1980",
	}
	if strings.Join(list, "\n") != strings.Join(want, "\n") {
		t.Errorf("comments:\n%s\nwant:\n%s", strings.Join(list, "\n"), strings.Join(want, "\n"))
	}
	m := mustReadFile(t, path)
	if m.Tag("replaygain_track_gain") != "-7.32 dB" || m.FrontCover() == nil {
		t.Errorf("reader: %v, cover %v", m.Tags, m.FrontCover() != nil)
	}
	if l := blockLayout(chainOf(t, path)); !strings.HasSuffix(l, " 1:"+strconv.Itoa(lastPadding(t, path))) {
		t.Errorf("layout %s: the last block is not the padding", l)
	}
}

func lastPadding(t *testing.T, path string) int {
	c := chainOf(t, path)
	b := c.blocks[len(c.blocks)-1]
	if b.typ != blockPadding {
		t.Fatalf("last block is type %d", b.typ)
	}
	return len(b.body)
}

// TestWriteTagsGrowsAFileWithNoPadding: nothing to give back, so the file is
// rewritten — longer by the edit plus a fresh PADDING block — and the audio
// after the new chain is the old audio, byte for byte.
func TestWriteTagsGrowsAFileWithNoPadding(t *testing.T) {
	path := taggedFixture(t, 0)
	size, audio := fileSize(t, path), audioDigest(t, path)
	pics := pictureBodies(t, path)
	long := strings.Repeat("a much longer title ", 20)
	if err := WriteTags(path, map[string]string{"TITLE": long, "GENRE": "New Wave"}, nil); err != nil {
		t.Fatal(err)
	}
	grew := fileSize(t, path) - size
	wantGrowth := int64(len(long)-len("Once in a Lifetime")) + int64(4+len("GENRE=New Wave")) + int64(blockHeaderLen+DefaultPadding)
	if grew != wantGrowth {
		t.Errorf("grew by %d, want %d", grew, wantGrowth)
	}
	if got := audioDigest(t, path); got != audio {
		t.Error("audio frames changed in the rewrite")
	}
	if got := pictureBodies(t, path); len(got) != 1 || !bytes.Equal(got[0], pics[0]) {
		t.Error("the cover changed")
	}
	if lastPadding(t, path) != DefaultPadding {
		t.Errorf("padding %d, want %d", lastPadding(t, path), DefaultPadding)
	}
	m := mustReadFile(t, path)
	if m.Tag("title") != long || m.Tag("genre") != "New Wave" || m.Tag("replaygain_track_peak") != "0.988403" {
		t.Errorf("tags after: %v", m.Tags)
	}
	_, list := rawComments(t, path)
	if list[len(list)-1] != "GENRE=New Wave" {
		t.Errorf("a new key is appended at the end: %q", list)
	}
	if l := tempLeftovers(t, filepath.Dir(path)); len(l) != 0 {
		t.Errorf("temporary files left behind: %v", l)
	}
	// And the next edit has room.
	size = fileSize(t, path)
	if err := WriteTags(path, map[string]string{"GENRE": "Post-punk"}, nil); err != nil {
		t.Fatal(err)
	}
	if fileSize(t, path) != size {
		t.Error("the second edit did not land in the padding the first one left")
	}
}

// A repeated key is replaced as a whole, at the place of its first comment;
// a removed key goes wherever it is; and WriteTagValues puts both back.
func TestWriteTagsRepeatedAndRemovedKeys(t *testing.T) {
	path := taggedFixture(t, 4096)
	before, err := ReadTagValues(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := before["artist"]; len(got) != 2 || got[0] != "Talking Heads" || got[1] != "Brian Eno" {
		t.Fatalf("ReadTagValues artist = %q", got)
	}
	if err := WriteTags(path, map[string]string{"ARTIST": "Talking Heads & Brian Eno"}, []string{"date"}); err != nil {
		t.Fatal(err)
	}
	_, list := rawComments(t, path)
	want := "TITLE=Once in a Lifetime|ARTIST=Talking Heads & Brian Eno|ALBUM=Remain in Light|" +
		"REPLAYGAIN_TRACK_GAIN=-7.32 dB|REPLAYGAIN_TRACK_PEAK=0.988403|" +
		"MUSICBRAINZ_TRACKID=4f8a2c1e-3b0a-4c6e-9d8f-0a1b2c3d4e5f|tracknumber=4"
	if got := strings.Join(list, "|"); got != want {
		t.Errorf("after:\n%s\nwant:\n%s", got, want)
	}
	if err := WriteTagValues(path, map[string][]string{"artist": before["artist"], "date": before["date"]}); err != nil {
		t.Fatal(err)
	}
	after, err := ReadTagValues(path)
	if err != nil {
		t.Fatal(err)
	}
	for k, vs := range before {
		if strings.Join(after[k], "|") != strings.Join(vs, "|") {
			t.Errorf("%s: %q, want %q", k, after[k], vs)
		}
	}
	if len(after) != len(before) {
		t.Errorf("keys %v, want %v", after, before)
	}
}

// A file with no comment block gets one, right after STREAMINFO.
func TestWriteTagsCreatesTheCommentBlock(t *testing.T) {
	data := BuildFileP(testInfo, nil, nil, 0)
	path := writeTemp(t, "bare.flac", data)
	c := chainOf(t, path)
	var blocks []metaBlock
	for _, b := range c.blocks {
		if b.typ != blockVorbisComment {
			blocks = append(blocks, b)
		}
	}
	out := append([]byte("fLaC"), encodeChain(blocks)...)
	out = append(out, data[c.audioOff:]...)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	audio := audioDigest(t, path)
	if err := WriteTags(path, map[string]string{"title": "Untitled"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := chainOf(t, path).blocks; got[1].typ != blockVorbisComment {
		t.Errorf("layout %s", blockLayout(chainOf(t, path)))
	}
	if mustReadFile(t, path).Tag("title") != "Untitled" || audioDigest(t, path) != audio {
		t.Error("title or audio wrong")
	}
}

// A truncated file, a bad key, a key both set and removed: an error, and not
// one byte of the file is written.
func TestWriteTagsRefusals(t *testing.T) {
	whole, err := os.ReadFile(taggedFixture(t, 4096))
	if err != nil {
		t.Fatal(err)
	}
	c := chainOf(t, writeTemp(t, "whole.flac", whole))
	cases := []struct {
		name   string
		data   []byte
		set    map[string]string
		remove []string
	}{
		{"cut in the padding", whole[:c.audioOff-100], map[string]string{"TITLE": "x"}, nil},
		{"cut in the comments", whole[:60], map[string]string{"TITLE": "x"}, nil},
		{"not a flac", []byte("ID3\x04junkjunkjunk"), map[string]string{"TITLE": "x"}, nil},
		{"key with =", whole, map[string]string{"TI=TLE": "x"}, nil},
		{"key with a control byte", whole, map[string]string{"TITLE\n": "x"}, nil},
		{"set and removed", whole, map[string]string{"TITLE": "x"}, []string{"title"}},
		{"twice by case", whole, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, "cut.flac", tc.data)
			var err error
			if tc.name == "twice by case" {
				err = WriteTagValues(path, map[string][]string{"Title": {"a"}, "TITLE": {"b"}})
			} else {
				err = WriteTags(path, tc.set, tc.remove)
			}
			if err == nil {
				t.Fatal("accepted")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(after, tc.data) {
				t.Errorf("the file was modified (%d -> %d bytes)", len(tc.data), len(after))
			}
			if l := tempLeftovers(t, filepath.Dir(path)); len(l) != 0 {
				t.Errorf("temporary files left behind: %v", l)
			}
		})
	}
}

// The outside opinion: ffprobe reads the new tags and the old ReplayGain,
// sees the cover, and ffmpeg decodes the file with nothing to say.
func TestWriteTagsAgreesWithFFprobe(t *testing.T) {
	ffprobe, ffmpeg := lookTool("ffprobe"), lookTool("ffmpeg")
	if ffprobe == "" || ffmpeg == "" {
		t.Skip("ffprobe/ffmpeg not installed")
	}
	for _, padding := range []int{4096, 0} {
		path := taggedFixture(t, padding)
		if err := WriteTags(path, map[string]string{"title": "Crosseyed and Painless", "ALBUMARTIST": "Talking Heads"}, nil); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "format_tags", "-of", "default=nw=1", path).Output()
		if err != nil {
			t.Fatal(err)
		}
		tags := strings.ToLower(string(out))
		for _, want := range []string{"tag:title=crosseyed and painless", "tag:album_artist=talking heads",
			"tag:replaygain_track_gain=-7.32 db", "tag:musicbrainz_trackid="} {
			if !strings.Contains(tags, want) {
				t.Errorf("padding %d: ffprobe tags lack %q:\n%s", padding, want, out)
			}
		}
		streams, err := probeStreams(ffprobe, path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(streams, "png,video") {
			t.Errorf("cover not seen: %q", streams)
		}
		decodeClean(t, ffmpeg, path)
	}
}
