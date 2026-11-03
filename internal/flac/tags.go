package flac

// tags.go writes Vorbis comments — the tags — on the same mechanism
// write.go uses for pictures: the metadata chain is rebuilt in memory and
// landed in place when it fits (the freed or consumed space is PADDING),
// or by a rewrite through a temporary file when it does not. The audio
// frames are never touched, and nothing in the comment block is rebuilt
// that the edit did not name: every other comment keeps its bytes, its
// spelling of the key and its place in the list, and the vendor string
// stays. That is the whole point — an editor that regenerates the block
// from the fields it knows loses the ReplayGain, the MusicBrainz ids and
// whatever else a ripper wrote.

import (
	"errors"
	"fmt"
	"os"
	"sort"
)

// WriteTags sets and removes Vorbis comments in the FLAC file at path.
//
// Keys are case-insensitive, as the format says: setting "title" replaces a
// "TITLE=" comment where it stands. A key that occurs more than once has all
// its comments replaced by the one value. A key in remove is dropped
// wherever it occurs (a key in both set and remove is an error). Keys not
// already in the file are appended, in sorted order, as upper case.
//
// Every other comment, the vendor string and every other block are kept
// byte for byte; the audio frames are never modified (see editChain). The
// file is read whole before anything is written, so a truncated or
// malformed file is an error and is left exactly as it was.
func WriteTags(path string, set map[string]string, remove []string, opts ...Options) error {
	values := make(map[string][]string, len(set)+len(remove))
	for k, v := range set {
		values[k] = []string{v}
	}
	for _, k := range remove {
		for s := range set {
			if asciiLower(s) == asciiLower(k) {
				return fmt.Errorf("flac: %q is both set and removed", k)
			}
		}
		values[k] = nil
	}
	return WriteTagValues(path, values, opts...)
}

// WriteTagValues is WriteTags for keys that carry more than one value (two
// ARTIST comments): each key's comments are replaced by its values, in
// order, at the place the first of them stood; a key with no values is
// removed. It is what an undo needs to put a multi-valued key back exactly.
func WriteTagValues(path string, values map[string][]string, opts ...Options) error {
	if len(values) == 0 {
		return nil
	}
	want := make(map[string][]string, len(values))
	spell := make(map[string]string, len(values))
	for k, vs := range values {
		if err := validKey(k); err != nil {
			return err
		}
		lk := asciiLower(k)
		if _, dup := want[lk]; dup {
			return fmt.Errorf("flac: key %q is given twice (keys are case-insensitive)", k)
		}
		want[lk], spell[lk] = vs, upperASCII(k)
	}
	return editChain(path, options(opts), func(blocks []metaBlock) ([]metaBlock, error) {
		out := keepBlocks(blocks, func(metaBlock) bool { return false })
		placed := map[string]bool{}
		found := false
		for i := range out {
			if out[i].typ != blockVorbisComment {
				continue
			}
			body, err := editComments(out[i].body, want, spell, placed, !found)
			if err != nil {
				return nil, err
			}
			out[i].body = body
			found = true
		}
		if !found {
			// No comment block at all: a new one, straight after
			// STREAMINFO, where every encoder puts it.
			body, err := editComments(emptyComments(), want, spell, placed, true)
			if err != nil {
				return nil, err
			}
			out = append(out[:1], append([]metaBlock{{typ: blockVorbisComment, body: body}}, out[1:]...)...)
		}
		for _, b := range out {
			if len(b.body) > maxBlockBody {
				return nil, fmt.Errorf("flac: a metadata block would be %d bytes, over the %d byte format limit", len(b.body), maxBlockBody)
			}
		}
		return out, nil
	})
}

// ReadTagValues returns every Vorbis comment in the file, keys lower-cased,
// values in file order — unlike Meta.Tags, which keeps the last of a
// repeated key. It is the "before" an undo restores.
func ReadTagValues(path string) (map[string][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	c, err := readChain(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := map[string][]string{}
	for _, b := range c.blocks {
		if b.typ != blockVorbisComment {
			continue
		}
		vc, err := splitComments(b.body)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for _, raw := range vc.comments {
			if k, v, ok := commentKV(raw); ok {
				out[k] = append(out[k], v)
			}
		}
	}
	return out, nil
}

// validKey is the Vorbis field-name rule: printable ASCII 0x20..0x7D, no '='.
func validKey(k string) error {
	if k == "" {
		return errors.New("flac: empty tag key")
	}
	for i := 0; i < len(k); i++ {
		if c := k[i]; c < 0x20 || c > 0x7d || c == '=' {
			return fmt.Errorf("flac: %q is not a valid Vorbis comment key", k)
		}
	}
	return nil
}

func upperASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'a' <= c && c <= 'z' {
			b[i] = c - 'a' + 'A'
		}
	}
	return string(b)
}

// vorbisComments is a VORBIS_COMMENT body split into its parts, each comment
// kept as the raw bytes it was.
type vorbisComments struct {
	vendor   []byte
	comments [][]byte
	tail     []byte // anything after the last comment; kept as it was
}

func emptyComments() []byte {
	const vendor = "core"
	b := appendU32LE(nil, uint32(len(vendor)))
	b = append(b, vendor...)
	return appendU32LE(b, 0)
}

func splitComments(body []byte) (*vorbisComments, error) {
	c := cursor{b: body}
	vendorLen, err := c.u32le("vendor length")
	if err != nil {
		return nil, err
	}
	vendor, err := c.take(vendorLen, "vendor string")
	if err != nil {
		return nil, err
	}
	count, err := c.u32le("comment count")
	if err != nil {
		return nil, err
	}
	out := &vorbisComments{vendor: vendor}
	for i := uint32(0); i < count; i++ {
		n, err := c.u32le(fmt.Sprintf("comment %d length", i))
		if err != nil {
			return nil, err
		}
		raw, err := c.take(n, fmt.Sprintf("comment %d", i))
		if err != nil {
			return nil, err
		}
		out.comments = append(out.comments, raw)
	}
	out.tail = body[c.i:]
	return out, nil
}

// commentKV splits one comment as parseVorbisComment does: the key before the
// first '=', lower-cased; ok is false for a comment the reader ignores.
func commentKV(raw []byte) (key, value string, ok bool) {
	eq := indexByte(raw, '=')
	if eq <= 0 {
		return "", "", false
	}
	return asciiLower(string(raw[:eq])), string(raw[eq+1:]), true
}

// editComments applies want to one comment block. placed carries across
// blocks (a file with two comment blocks has the new value written once, in
// the first); appendNew says this block takes the keys nobody had.
func editComments(body []byte, want map[string][]string, spell map[string]string,
	placed map[string]bool, appendNew bool) ([]byte, error) {
	vc, err := splitComments(body)
	if err != nil {
		return nil, err
	}
	var kept [][]byte
	emit := func(lk string) {
		for _, v := range want[lk] {
			kept = append(kept, []byte(spell[lk]+"="+v))
		}
		placed[lk] = true
	}
	for _, raw := range vc.comments {
		k, _, ok := commentKV(raw)
		if _, edited := want[k]; !ok || !edited {
			kept = append(kept, raw)
			continue
		}
		if !placed[k] {
			emit(k)
		}
	}
	if appendNew {
		var rest []string
		for lk := range want {
			if !placed[lk] {
				rest = append(rest, lk)
			}
		}
		sort.Strings(rest)
		for _, lk := range rest {
			emit(lk)
		}
	}
	out := appendU32LE(nil, uint32(len(vc.vendor)))
	out = append(out, vc.vendor...)
	out = appendU32LE(out, uint32(len(kept)))
	for _, raw := range kept {
		out = appendU32LE(out, uint32(len(raw)))
		out = append(out, raw...)
	}
	return append(out, vc.tail...), nil
}
