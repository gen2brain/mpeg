//go:build riscv64 && riscv64.rva23u64 && !noasm

package mpeg

import "testing"

func TestIdctRVV(t *testing.T) {
	runIdctParity(t, idctRVV)
}
