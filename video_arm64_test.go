//go:build arm64 && !noasm

package mpeg

import "testing"

func TestIdctNEON(t *testing.T) {
	runIdctParity(t, idctNEON)
}
