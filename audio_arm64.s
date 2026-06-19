//go:build arm64 && !noasm

#include "textflag.h"

// synthWindowNEON applies the audio synthesis window: u[0:32] +=
// d[dIndex:+32]*v[vIndex:+32], dIndex += 64 and vIndex += 128 per pass, two
// passes, accumulating in V0..V7 throughout. FMLA matches the FMA the compiler
// contracts the scalar fallback to on arm64.
//
// func synthWindowNEON(u *[32]float32, d *[1024]float32, v *[1024]float32, vPos int)
//   R0=u R1=d R2=v R3=vPos  R8=vIndex R9=dIndex  R10=d+dIndex R11=v+vIndex R12=scratch

// FMAC: acc += d * v, 4 floats.
#define FMAC(acc)            \
	VLD1 (R10), [V16.S4]     \
	ADD  $16, R10            \
	VLD1 (R11), [V17.S4]     \
	ADD  $16, R11            \
	VFMLA V17.S4, V16.S4, acc

// FMACBLOCK: one 32-lane tap block.
#define FMACBLOCK    \
	LSL $2, R9, R12  \
	ADD R12, R1, R10 \
	LSL $2, R8, R12  \
	ADD R12, R2, R11 \
	FMAC(V0.S4)      \
	FMAC(V1.S4)      \
	FMAC(V2.S4)      \
	FMAC(V3.S4)      \
	FMAC(V4.S4)      \
	FMAC(V5.S4)      \
	FMAC(V6.S4)      \
	FMAC(V7.S4)

TEXT ·synthWindowNEON(SB), NOSPLIT, $0-32
	MOVD u+0(FP), R0
	MOVD d+8(FP), R1
	MOVD v+16(FP), R2
	MOVD vPos+24(FP), R3

	VEOR V0.B16, V0.B16, V0.B16
	VEOR V1.B16, V1.B16, V1.B16
	VEOR V2.B16, V2.B16, V2.B16
	VEOR V3.B16, V3.B16, V3.B16
	VEOR V4.B16, V4.B16, V4.B16
	VEOR V5.B16, V5.B16, V5.B16
	VEOR V6.B16, V6.B16, V6.B16
	VEOR V7.B16, V7.B16, V7.B16

	// dIndex = 512 - (vPos>>1)
	LSR  $1, R3, R12
	MOVD $512, R9
	SUB  R12, R9, R9

	// vIndex = (vPos & 127) >> 1
	AND $127, R3, R12
	LSR $1, R12, R8

neon_loop1:
	CMP  $1024, R8
	BGE  neon_loop1done
	FMACBLOCK
	ADD  $128, R8
	ADD  $64, R9
	B    neon_loop1

neon_loop1done:
	SUB  $480, R9, R9   // dIndex -= 512-32
	MOVD $1120, R12     // vIndex = (128-32+1024) - vIndex
	SUB  R8, R12, R8

neon_loop2:
	CMP  $1024, R8
	BGE  neon_loop2done
	FMACBLOCK
	ADD  $128, R8
	ADD  $64, R9
	B    neon_loop2

neon_loop2done:
	VST1 [V0.S4, V1.S4, V2.S4, V3.S4], (R0)
	ADD  $64, R0
	VST1 [V4.S4, V5.S4, V6.S4, V7.S4], (R0)
	RET
