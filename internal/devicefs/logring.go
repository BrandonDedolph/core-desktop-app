// SPDX-License-Identifier: Apache-2.0

package devicefs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"sort"
	"strings"
)

// The ring half of CORELOG.BIN: the blocks the DEVICE writes. log.go is the
// header, which only the host writes; this file reads what came back.
//
// Ring block (little-endian), core/kernel/evlog.h "BLOCK FORMAT", mirrored by
// tools/make_log.py encode_block/decode_block:
//
//	off  size  field
//	0    4     magic   'C''L''O''B'  (0x424F4C43 LE)
//	4    4     seq     monotonic across boots, 0 in a fresh file
//	8    2     boot    boot id: the previous newest block's + 1
//	10   2     len     bits 0..14 = text bytes, bit 15 = FINAL (a forced flush)
//	12   4     crc32   zlib CRC-32 over bytes [0,12) and [16,2048)
//	16   2032  text
//
// Block i of the file (1 .. count-1) holds the seq with 1 + seq%(count-1) == i.
// Unwritten slots are zero (no magic); a torn write fails its CRC. Both are
// simply absent from what LogBlocks returns.
const (
	LogBlockMagic   = 0x424F4C43 // 'C''L''O''B' LE
	LogBlockHdr     = 16
	LogBlockText    = LogBlockBytes - LogBlockHdr // 2032
	logLenMask      = 0x7FFF
	logLenFinal     = 0x8000
	logBlkOffSeq    = 4
	logBlkOffBoot   = 8
	logBlkOffLen    = 10
	logBlkOffCRC    = 12
	logBlkCRCPrefix = 12
)

// LogBlock is one decoded ring block.
type LogBlock struct {
	Seq   uint32
	Boot  uint16
	Final bool
	Text  []byte
}

// LogRing is a whole CORELOG.BIN as the host reads it back.
type LogRing struct {
	Blocks uint32 // the header's count, header included
	FileID uint32
	// Entries are the valid ring blocks in seq order — oldest first,
	// newest last — whatever slot the ring put them in.
	Entries []LogBlock
}

// ErrLogHeader is a file whose block 0 does not validate.
var ErrLogHeader = errors.New("no valid CLOG header in block 0")

// logBlockCRC is the CRC the firmware stores: the 12 header bytes before the
// CRC field, then the text area, skipping the field itself.
func logBlockCRC(blk []byte) uint32 {
	c := crc32.ChecksumIEEE(blk[:logBlkCRCPrefix])
	return crc32.Update(c, crc32.IEEETable, blk[LogBlockHdr:LogBlockBytes])
}

// EncodeLogBlock builds one ring block, zero-padded. It is what the device
// writes; the host only needs it for fixtures. Mirrors evlog_block_encode().
func EncodeLogBlock(seq uint32, boot uint16, text []byte, final bool) ([LogBlockBytes]byte, error) {
	var blk [LogBlockBytes]byte
	if len(text) > LogBlockText {
		return blk, fmt.Errorf("devicefs: %d bytes of log text, a block holds %d", len(text), LogBlockText)
	}
	n := uint16(len(text))
	if final {
		n |= logLenFinal
	}
	binary.LittleEndian.PutUint32(blk[0:], LogBlockMagic)
	binary.LittleEndian.PutUint32(blk[logBlkOffSeq:], seq)
	binary.LittleEndian.PutUint16(blk[logBlkOffBoot:], boot)
	binary.LittleEndian.PutUint16(blk[logBlkOffLen:], n)
	copy(blk[LogBlockHdr:], text)
	binary.LittleEndian.PutUint32(blk[logBlkOffCRC:], logBlockCRC(blk[:]))
	return blk, nil
}

// DecodeLogBlock validates one ring block: magic, length, CRC — the checks
// evlog_block_decode() makes. ok is false for an unwritten or torn slot. Text
// aliases blk.
func DecodeLogBlock(blk []byte) (LogBlock, bool) {
	if len(blk) < LogBlockBytes {
		return LogBlock{}, false
	}
	if binary.LittleEndian.Uint32(blk[0:]) != LogBlockMagic {
		return LogBlock{}, false
	}
	l := binary.LittleEndian.Uint16(blk[logBlkOffLen:])
	n := int(l & logLenMask)
	if n > LogBlockText {
		return LogBlock{}, false
	}
	if logBlockCRC(blk) != binary.LittleEndian.Uint32(blk[logBlkOffCRC:]) {
		return LogBlock{}, false
	}
	return LogBlock{
		Seq:   binary.LittleEndian.Uint32(blk[logBlkOffSeq:]),
		Boot:  binary.LittleEndian.Uint16(blk[logBlkOffBoot:]),
		Final: l&logLenFinal != 0,
		Text:  blk[LogBlockHdr : LogBlockHdr+n],
	}, true
}

// LogBlockIndex is the block a sequence number lands in (evlog_block_index).
func LogBlockIndex(seq, count uint32) uint32 { return 1 + seq%(count-1) }

// ReadLogRing decodes a whole CORELOG.BIN image: the header, then every valid
// ring block, sorted by seq. It is make_log.py's ordered_entries(). A file
// shorter than its header claims is read as far as it goes — the blocks that
// are there are still the device's words — but a bad header is an error,
// because then nothing says where the blocks are.
func ReadLogRing(data []byte) (LogRing, error) {
	count, fid, ok := DecodeLogHeader(data)
	if !ok {
		return LogRing{}, ErrLogHeader
	}
	r := LogRing{Blocks: count, FileID: fid}
	for i := uint32(1); i < count; i++ {
		off := int64(i) * LogBlockBytes
		if off+LogBlockBytes > int64(len(data)) {
			break
		}
		if b, ok := DecodeLogBlock(data[off : off+LogBlockBytes]); ok {
			r.Entries = append(r.Entries, b)
		}
	}
	// Numerically by seq, as the dump tool does: a u32 wrap is 8 TB of log.
	sort.SliceStable(r.Entries, func(a, b int) bool { return r.Entries[a].Seq < r.Entries[b].Seq })
	return r, nil
}

// Newest is the most recently written block, or false for an empty ring.
func (r LogRing) Newest() (LogBlock, bool) {
	if len(r.Entries) == 0 {
		return LogBlock{}, false
	}
	return r.Entries[len(r.Entries)-1], true
}

// LogEntry is one ring block the way a person reads it: the order and the
// text make_log.py --dump prints (ordered_entries, render_dump).
type LogEntry struct {
	Seq   uint32
	Boot  uint16
	Final bool
	// Missing is how many sequence numbers lie between this block and
	// the one before it — blocks the ring has overwritten, or torn ones
	// that failed their CRC. 0 for the first block and for a block that
	// follows on. It is the dump's "N block(s) missing" line.
	Missing int64
	// Text is the block's text decoded as the dump decodes it: UTF-8
	// with each ill-formed sequence replaced by U+FFFD, and CRLF read as
	// LF. It keeps its trailing newline, or lack of one.
	Text string
}

// OrderedLogEntries is the ring as make_log.py's ordered_entries hands it to
// render_dump: by seq, oldest first, each block's text decoded.
func OrderedLogEntries(r LogRing) []LogEntry {
	out := make([]LogEntry, 0, len(r.Entries))
	for i, b := range r.Entries {
		e := LogEntry{Seq: b.Seq, Boot: b.Boot, Final: b.Final, Text: LogText(b.Text)}
		if i > 0 {
			if prev := r.Entries[i-1].Seq; b.Seq != prev+1 {
				e.Missing = int64(b.Seq) - int64(prev) - 1
			}
		}
		out = append(out, e)
	}
	return out
}

// LogText decodes one block's text exactly as the dump tool does —
// Python's text.decode("utf-8", errors="replace").replace("\r\n", "\n").
//
// The replacement is per MAXIMAL SUBPART (Unicode 15, §3.9 "U+FFFD
// Substitution of Maximal Subparts", which CPython follows): a sequence
// cut short — and a 2032-byte block can end in the middle of a song
// title's é — is ONE U+FFFD, not one per byte as strings.ToValidUTF8's
// runs or utf8.DecodeRune's single bytes would give.
func LogText(b []byte) string {
	var s []byte
	for i := 0; i < len(b); {
		c := b[i]
		if c < 0x80 {
			s = append(s, c)
			i++
			continue
		}
		n, lo, hi := utf8Lead(c)
		k := 0
		if n > 0 {
			k = 1
			if i+1 < len(b) && b[i+1] >= lo && b[i+1] <= hi {
				k = 2
				for k < n && i+k < len(b) && b[i+k] >= 0x80 && b[i+k] <= 0xBF {
					k++
				}
			}
		}
		if n > 0 && k == n {
			s = append(s, b[i:i+n]...)
			i += n
			continue
		}
		s = append(s, "�"...)
		if k == 0 {
			k = 1
		}
		i += k
	}
	return strings.ReplaceAll(string(s), "\r\n", "\n")
}

// utf8Lead is Unicode Table 3-7 for one lead byte: the sequence's length
// and the range its SECOND byte must fall in (the rest are 80..BF). n is 0
// for a byte that cannot start a sequence.
func utf8Lead(c byte) (n int, lo, hi byte) {
	switch {
	case c >= 0xC2 && c <= 0xDF:
		return 2, 0x80, 0xBF
	case c == 0xE0:
		return 3, 0xA0, 0xBF
	case c == 0xED:
		return 3, 0x80, 0x9F
	case c >= 0xE1 && c <= 0xEF:
		return 3, 0x80, 0xBF
	case c == 0xF0:
		return 4, 0x90, 0xBF
	case c >= 0xF1 && c <= 0xF3:
		return 4, 0x80, 0xBF
	case c == 0xF4:
		return 4, 0x80, 0x8F
	}
	return 0, 0, 0
}
