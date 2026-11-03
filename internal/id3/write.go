package id3

// write.go is the other half of this package: changing an MP3's ID3v2 text
// frames. It obeys the rule internal/flac's writers do — the audio is never
// rewritten unless the whole file is, and then it is copied byte for byte —
// and one more: a frame the edit does not name keeps its bytes, its flags
// and its place. APIC (the cover), TXXX (ReplayGain, MusicBrainz ids),
// COMM, USLT and every frame this package cannot even read go through
// untouched.
//
// An MP3 has no container, so "the audio" is everything after the ID3v2
// tag at the head of the file: the frames, any ID3v1 or APE tail, a second
// appended ID3v2 tag. All of it is kept.
//
//   in place   the new frames fit in the old tag's size: the tag is
//              rewritten in its own bytes with the rest as zero padding, and
//              not one byte after it is written.
//   rewrite    otherwise (or the file has no tag): a new file beside the
//              old one — the new tag, fresh padding, then the old file from
//              the end of its tag — fsynced and renamed over it.
//
// What is refused, untouched: a file that is not an MP3, a tag whose size
// runs past the end of the file or whose frames do not add up, an ID3v2.2
// tag (three-letter frame ids; a tagger from 2002 should convert it, not
// this), and a tag with whole-tag unsynchronisation (the frame sizes no
// longer describe the bytes, which is why the reader treats it as opaque
// too). A v2.3 extended header and a v2.4 footer are dropped: both are
// optional, and the extended header's CRC would be wrong once a frame
// changes.

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"unicode/utf8"
)

// DefaultPadding is the zero padding written after the frames when the file
// has to be rewritten, so the next edit lands in place.
const DefaultPadding = 2048

// writeFrames maps each key WriteTags takes — ffprobe's names, the same as
// Meta.Tags — to the frame ids that carry it. Every id in the list is
// replaced; the first is the one written ("date" is TDRC in v2.4 and TYER
// in v2.3, see frameFor).
var writeFrames = map[string][]string{
	"title":        {"TIT2"},
	"artist":       {"TPE1"},
	"album":        {"TALB"},
	"album_artist": {"TPE2"},
	"genre":        {"TCON"},
	"date":         {"TDRC", "TYER"},
	"track":        {"TRCK"},
	"disc":         {"TPOS"},
	"composer":     {"TCOM"},
}

// WritableKeys are the keys WriteTags accepts, sorted.
func WritableKeys() []string {
	out := make([]string, 0, len(writeFrames))
	for k := range writeFrames {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func frameFor(key string, major byte) string {
	if key == "date" && major == 3 {
		return "TYER"
	}
	return writeFrames[key][0]
}

// testHookBeforeRename runs in the rewrite path once the temporary file is
// complete but before it replaces the original. Tests only.
var testHookBeforeRename func(tmpPath string) error

// WriteTags sets the text frames for the keys in set (see WritableKeys); an
// empty value removes the key's frames. Every other frame, and every byte
// after the tag, is kept.
//
// A file with no ID3v2 tag gets a v2.3 one. A v2.3 tag stays v2.3 (text in
// ISO-8859-1 when it fits, else UTF-16 with a BOM) and a v2.4 tag stays v2.4
// (UTF-8).
//
// The modification time is not preserved: the change has to be visible to
// the sync, which compares it.
func WriteTags(path string, set map[string]string) error {
	if len(set) == 0 {
		return nil
	}
	for k, v := range set {
		if _, ok := writeFrames[k]; !ok {
			return fmt.Errorf("id3: %q is not a key WriteTags writes", k)
		}
		if !utf8.ValidString(v) {
			return fmt.Errorf("id3: the %s value is not valid UTF-8", k)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	tag, err := splitTag(b)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	// The audio has to be there before anything is written: a file that is
	// not an MP3 is not tagged as one.
	if err := scanStream(b, tag.end, len(b)-tailTagLen(b[tag.end:]), &Meta{}); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	frames := editFrames(tag, set)
	var body []byte
	for _, f := range frames {
		body = append(body, f...)
	}
	if tag.end > 0 && 10+len(body) <= tag.end {
		// In place: the old tag's bytes, nothing after them.
		out := tagHeader(tag.major, tag.end-10)
		out = append(out, body...)
		out = append(out, make([]byte, tag.end-len(out))...)
		return writeAt(path, out)
	}
	out := tagHeader(tag.major, len(body)+DefaultPadding)
	out = append(out, body...)
	out = append(out, make([]byte, DefaultPadding)...)
	return rewrite(path, out, b[tag.end:])
}

// tagHeader is a ten-byte ID3v2 header: no flags, the syncsafe size.
func tagHeader(major byte, size int) []byte {
	return []byte{'I', 'D', '3', major, 0, 0,
		byte(size>>21) & 0x7f, byte(size>>14) & 0x7f, byte(size>>7) & 0x7f, byte(size) & 0x7f}
}

// headTag is the ID3v2 tag at the head of a file, split into raw frames.
type headTag struct {
	major  byte
	end    int // the first byte after the tag (0: there is none)
	frames [][]byte
}

func splitTag(b []byte) (*headTag, error) {
	if len(b) < 3 || string(b[:3]) != "ID3" {
		return &headTag{major: 3}, nil
	}
	if len(b) < 10 {
		return nil, errors.New("id3: truncated ID3v2 header")
	}
	n := id3v2One(b)
	if n == 0 {
		return nil, errors.New("id3: the ID3v2 header is not one this writer can read")
	}
	if n > len(b) {
		return nil, fmt.Errorf("id3: the ID3v2 tag says %d bytes and the file is %d: truncated", n, len(b))
	}
	major, flags := b[3], b[5]
	switch {
	case major == 2:
		return nil, errors.New("id3: an ID3v2.2 tag; convert it to v2.3 or v2.4 with a tagger first")
	case flags&0x80 != 0:
		return nil, errors.New("id3: the tag is unsynchronised as a whole; not rewritten")
	}
	t := &headTag{major: major, end: n}
	p, limit := 10, n
	if flags&0x10 != 0 {
		limit -= 10 // the v2.4 footer
	}
	if flags&0x40 != 0 {
		if p+4 > limit {
			return nil, errors.New("id3: truncated extended header")
		}
		ext := int(binary.BigEndian.Uint32(b[p : p+4]))
		if major >= 4 {
			ext = int(syncsafe(b[p:p+4])) - 4
		}
		p += 4
		if ext < 0 || p+ext > limit {
			return nil, errors.New("id3: the extended header runs past the tag")
		}
		p += ext
	}
	for p+10 <= limit && b[p] != 0 {
		var size int
		if major >= 4 {
			if (b[p+4]|b[p+5]|b[p+6]|b[p+7])&0x80 != 0 {
				return nil, fmt.Errorf("id3: frame %q has a size that is not syncsafe", b[p:p+4])
			}
			size = int(syncsafe(b[p+4 : p+8]))
		} else {
			size = int(binary.BigEndian.Uint32(b[p+4 : p+8]))
		}
		if size > limit-p-10 {
			return nil, fmt.Errorf("id3: frame %q runs past the end of the tag", b[p:p+4])
		}
		t.frames = append(t.frames, b[p:p+10+size])
		p += 10 + size
	}
	// Whatever follows the frames is padding; a tag that has stray bytes
	// there rather than zeros is still read the same way by everyone, so
	// they go.
	return t, nil
}

// editFrames is the tag's frames with set applied: a replaced key's new
// frame stands where its first old frame stood, its other frames go, and a
// key the tag did not have is appended.
func editFrames(t *headTag, set map[string]string) [][]byte {
	owner := map[string]string{} // frame id -> key
	for k := range set {
		for _, id := range writeFrames[k] {
			owner[id] = k
		}
	}
	done := map[string]bool{}
	var out [][]byte
	for _, f := range t.frames {
		k, edited := owner[string(f[:4])]
		if !edited {
			out = append(out, f)
			continue
		}
		if !done[k] {
			done[k] = true
			if v := set[k]; v != "" {
				out = append(out, textFrame(frameFor(k, t.major), v, t.major))
			}
		}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !done[k] && set[k] != "" {
			out = append(out, textFrame(frameFor(k, t.major), set[k], t.major))
		}
	}
	return out
}

// textFrame encodes one text frame: UTF-8 in v2.4; in v2.3, ISO-8859-1 when
// every character fits and UTF-16 with a BOM when one does not.
func textFrame(id, v string, major byte) []byte {
	var body []byte
	switch {
	case major >= 4:
		body = append([]byte{3}, v...)
	case latin1(v):
		body = []byte{0}
		for _, r := range v {
			body = append(body, byte(r))
		}
	default:
		body = []byte{1, 0xff, 0xfe}
		for _, r := range v {
			if r >= 0x10000 {
				r -= 0x10000
				hi, lo := 0xd800+(r>>10), 0xdc00+(r&0x3ff)
				body = append(body, byte(hi), byte(hi>>8), byte(lo), byte(lo>>8))
				continue
			}
			body = append(body, byte(r), byte(r>>8))
		}
	}
	n := len(body)
	f := []byte(id)
	if major >= 4 {
		f = append(f, byte(n>>21)&0x7f, byte(n>>14)&0x7f, byte(n>>7)&0x7f, byte(n)&0x7f)
	} else {
		f = binary.BigEndian.AppendUint32(f, uint32(n))
	}
	f = append(f, 0, 0)
	return append(f, body...)
}

func latin1(s string) bool {
	for _, r := range s {
		if r > 0xff {
			return false
		}
	}
	return true
}

// writeAt overwrites the head of the file with tag, which is exactly as long
// as the tag it replaces.
func writeAt(path string, tag []byte) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if _, err := f.WriteAt(tag, 0); err != nil {
		f.Close()
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("%s: %w", path, err)
	}
	return f.Close()
}

// rewrite writes tag + rest beside path and renames it over the top. Until
// the rename the original is complete and playable.
func rewrite(path string, tag, rest []byte) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, "."+base+".core-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	done := false
	defer func() {
		if !done {
			tmp.Close()
			os.Remove(name)
		}
	}()
	w := bufio.NewWriterSize(tmp, 1<<20)
	if _, err := w.Write(tag); err != nil {
		return err
	}
	if _, err := w.Write(rest); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	_ = tmp.Chmod(st.Mode().Perm())
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if testHookBeforeRename != nil {
		if err := testHookBeforeRename(name); err != nil {
			return err
		}
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	done = true
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}
