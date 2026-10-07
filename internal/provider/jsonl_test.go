// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package provider

import (
	"bufio"
	"bytes"
	"io"
	"testing"
)

func TestReadJSONLLineSkipsOversizedRecordAndContinues(t *testing.T) {
	input := append(bytes.Repeat([]byte("x"), MaxJSONLLineBytes+1), '\n')
	input = append(input, []byte("next\n")...)
	br := bufio.NewReader(bytes.NewReader(input))

	line, err := ReadJSONLLine(br)
	if line != nil || err != nil {
		t.Fatalf("first ReadJSONLLine() = %q, %v; want nil, nil", line, err)
	}
	line, err = ReadJSONLLine(br)
	if err != nil {
		t.Fatalf("second ReadJSONLLine(): %v", err)
	}
	if got, want := string(line), "next"; got != want {
		t.Errorf("second line = %q, want %q", got, want)
	}
	_, err = ReadJSONLLine(br)
	if err != io.EOF {
		t.Errorf("final ReadJSONLLine() error = %v, want io.EOF", err)
	}
}
