//go:build amd64 && !noasm

#include "textflag.h"

// synthWindow{SSE2,AVX2} apply the audio synthesis window: u[0:32] +=
// d[dIndex:+32]*v[vIndex:+32], dIndex += 64 and vIndex += 128 per pass, two
// passes, accumulating in registers throughout. SSE2 has no FMA (matches the
// scalar fallback bit-for-bit); AVX2 fuses.
//
// func synthWindow{SSE2,AVX2}(u *[32]float32, d *[1024]float32, v *[1024]float32, vPos int)
//   DI=u SI=d BX=v  R8=vIndex R9=dIndex  R10=d+dIndex R11=v+vIndex

// MAC: acc += d[off] * v[off], 4 floats.
#define MAC(off, acc)        \
	MOVUPS off(R11), X8      \
	MOVUPS off(R10), X9      \
	MULPS  X9, X8            \
	ADDPS  X8, acc

// MACBLOCK: one 32-lane tap block.
#define MACBLOCK    \
	LEAQ (SI)(R9*4), R10 \
	LEAQ (BX)(R8*4), R11 \
	MAC(0, X0)           \
	MAC(16, X1)          \
	MAC(32, X2)          \
	MAC(48, X3)          \
	MAC(64, X4)          \
	MAC(80, X5)          \
	MAC(96, X6)          \
	MAC(112, X7)

TEXT ·synthWindowSSE2(SB), NOSPLIT, $0-32
	MOVQ u+0(FP), DI
	MOVQ d+8(FP), SI
	MOVQ v+16(FP), BX
	MOVQ vPos+24(FP), CX

	XORPS X0, X0
	XORPS X1, X1
	XORPS X2, X2
	XORPS X3, X3
	XORPS X4, X4
	XORPS X5, X5
	XORPS X6, X6
	XORPS X7, X7

	// dIndex = 512 - (vPos>>1)
	MOVQ CX, AX
	SHRQ $1, AX
	MOVQ $512, R9
	SUBQ AX, R9

	// vIndex = (vPos & 127) >> 1
	MOVQ CX, AX
	ANDQ $127, AX
	SHRQ $1, AX
	MOVQ AX, R8

sse_loop1:
	CMPQ R8, $1024
	JGE  sse_loop1done
	MACBLOCK
	ADDQ $128, R8
	ADDQ $64, R9
	JMP  sse_loop1

sse_loop1done:
	SUBQ $480, R9       // dIndex -= 512-32
	MOVQ $1120, AX      // vIndex = (128-32+1024) - vIndex
	SUBQ R8, AX
	MOVQ AX, R8

sse_loop2:
	CMPQ R8, $1024
	JGE  sse_loop2done
	MACBLOCK
	ADDQ $128, R8
	ADDQ $64, R9
	JMP  sse_loop2

sse_loop2done:
	MOVUPS X0, 0(DI)
	MOVUPS X1, 16(DI)
	MOVUPS X2, 32(DI)
	MOVUPS X3, 48(DI)
	MOVUPS X4, 64(DI)
	MOVUPS X5, 80(DI)
	MOVUPS X6, 96(DI)
	MOVUPS X7, 112(DI)
	RET

// VMAC: acc += d[off] * v[off], 8 floats, fused.
#define VMAC(off, acc)         \
	VMOVUPS off(R11), Y4       \
	VMOVUPS off(R10), Y5       \
	VFMADD231PS Y5, Y4, acc

#define VMACBLOCK    \
	LEAQ (SI)(R9*4), R10 \
	LEAQ (BX)(R8*4), R11 \
	VMAC(0, Y0)          \
	VMAC(32, Y1)         \
	VMAC(64, Y2)         \
	VMAC(96, Y3)

TEXT ·synthWindowAVX2(SB), NOSPLIT, $0-32
	MOVQ u+0(FP), DI
	MOVQ d+8(FP), SI
	MOVQ v+16(FP), BX
	MOVQ vPos+24(FP), CX

	VXORPS Y0, Y0, Y0
	VXORPS Y1, Y1, Y1
	VXORPS Y2, Y2, Y2
	VXORPS Y3, Y3, Y3

	MOVQ CX, AX
	SHRQ $1, AX
	MOVQ $512, R9
	SUBQ AX, R9

	MOVQ CX, AX
	ANDQ $127, AX
	SHRQ $1, AX
	MOVQ AX, R8

avx_loop1:
	CMPQ R8, $1024
	JGE  avx_loop1done
	VMACBLOCK
	ADDQ $128, R8
	ADDQ $64, R9
	JMP  avx_loop1

avx_loop1done:
	SUBQ $480, R9
	MOVQ $1120, AX
	SUBQ R8, AX
	MOVQ AX, R8

avx_loop2:
	CMPQ R8, $1024
	JGE  avx_loop2done
	VMACBLOCK
	ADDQ $128, R8
	ADDQ $64, R9
	JMP  avx_loop2

avx_loop2done:
	VMOVUPS Y0, 0(DI)
	VMOVUPS Y1, 32(DI)
	VMOVUPS Y2, 64(DI)
	VMOVUPS Y3, 96(DI)
	VZEROUPPER
	RET
