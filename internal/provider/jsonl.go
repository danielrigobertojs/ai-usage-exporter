// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package provider

import (
	"bufio"
	"bytes"
	"io"
)

// MaxJSONLLineBytes bounds the memory retained for one JSONL record. A
// longer record is consumed but returned as nil so the caller can keep
// scanning the following records without retaining unbounded input.
const MaxJSONLLineBytes = 8 << 20 // 8 MiB

// ReadJSONLLine reads one newline-terminated (or EOF-terminated) JSONL
// record from br, trimming its line ending. Oversized records are fully
// consumed and returned as nil; io.EOF is returned only once br is exhausted
// (possibly with the final, unterminated record).
func ReadJSONLLine(br *bufio.Reader) (line []byte, err error) {
	var buf []byte
	oversized := false
	for {
		chunk, rerr := br.ReadSlice('\n')
		if !oversized {
			if len(buf)+len(chunk) > MaxJSONLLineBytes {
				oversized = true
				buf = nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		switch rerr {
		case nil:
			if oversized {
				return nil, nil
			}
			return bytes.TrimSuffix(bytes.TrimSuffix(buf, []byte("\n")), []byte("\r")), nil
		case bufio.ErrBufferFull:
			continue
		case io.EOF:
			if oversized || len(buf) == 0 {
				return nil, io.EOF
			}
			return bytes.TrimSuffix(buf, []byte("\r")), io.EOF
		default:
			return nil, rerr
		}
	}
}
