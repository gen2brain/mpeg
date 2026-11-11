//go:build riscv64 && riscv64.rva23u64 && !noasm

package mpeg

import "testing"

func TestSynthWindowRVV(t *testing.T) {
	runSynthWindowParity(t, synthWindowRVV, 0)
}
