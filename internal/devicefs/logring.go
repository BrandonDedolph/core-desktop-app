// SPDX-License-Identifier: Apache-2.0

package devicefs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"sort"
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
