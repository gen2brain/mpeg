//go:build riscv64 && riscv64.rva23u64 && !noasm

package mpeg

//go:noescape
func synthWindowRVV(u *[32]float32, d *[1024]float32, v *[1024]float32, vPos int)

func synthWindow(u *[32]float32, d *[1024]float32, v *[1024]float32, vPos int) {
	synthWindowRVV(u, d, v, vPos)
}
