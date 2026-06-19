//go:build arm64 && !noasm

package mpeg

import "testing"

func TestSynthWindowNEON(t *testing.T) {
	runSynthWindowParity(t, synthWindowNEON, 0)
}
