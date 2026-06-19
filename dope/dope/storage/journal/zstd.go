package journal

import (
	"fmt"

	"github.com/klauspost/compress/zstd"
)

// The journal compresses cold segments with zstd. A single shared encoder/
// decoder pair is reused across calls (allocating one per call is wasteful of
// the coder state). The decoder reads any zstd stream regardless of the level
// it was written at, so segments written by earlier code still decode.
var (
	zEnc *zstd.Encoder
	zDec *zstd.Decoder
)

func init() {
	var err error
	zEnc, err = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBetterCompression))
	if err != nil {
		panic(fmt.Sprintf("journal: init zstd encoder: %v", err))
	}
	zDec, err = zstd.NewReader(nil)
	if err != nil {
		panic(fmt.Sprintf("journal: init zstd decoder: %v", err))
	}
}

// Compress zstd-compresses a (pre-encoded) segment stream.
func Compress(raw []byte) []byte { return zEnc.EncodeAll(raw, nil) }

// Decompress inflates a zstd-compressed segment stream.
func Decompress(comp []byte) ([]byte, error) { return zDec.DecodeAll(comp, nil) }
