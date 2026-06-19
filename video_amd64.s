//go:build amd64 && !noasm

#include "textflag.h"

// copyMacroblockSSE2 implements MPEG-1 block motion compensation using SSE2.
// It supports direct copy, horizontal, vertical, and bilinear interpolation
// for luma (16x16) and chroma (8x8) blocks, with rounding as per MPEG spec.
//
// func copyMacroblockSSE2(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame)
//
// Half-pel along a single axis is the rounding average (a+b+1)>>1, which PAVGB
// computes per byte directly. The bilinear case needs (a+b+c+d+2)>>2, which is
// done by widening bytes to words, summing, adding 2, shifting, and packing.
//
// Persistent registers:
//   R8  = lumaWidth (luma stride)
//   R9  = chromaWidth (chroma stride)
//   R10 = source frame, then s.Cr.Data
//   R11 = dest frame, then d.Cr.Data
//   X13 = 0x0002 per word (bilinear rounding bias)
//   X15 = 0x0000 (zero, for unpack/pack)
TEXT ·copyMacroblockSSE2(SB), NOSPLIT, $0-64
	MOVQ motionH+0(FP), AX
	MOVQ motionV+8(FP), BX
	MOVQ mbRow+16(FP), CX
	MOVQ mbCol+24(FP), DX
	MOVQ lumaWidth+32(FP), R8
	MOVQ chromaWidth+40(FP), R9
	MOVQ s+48(FP), R10
	MOVQ d+56(FP), R11

	PXOR    X15, X15  // zero
	PCMPEQW X13, X13
	PSRLW   $15, X13  // 0x0001 per word
	PADDW   X13, X13  // 0x0002 per word

	// Split the luma motion vector into integer offset and half-pel fraction.
	MOVQ AX, R14
	ANDQ $1, R14 // R14 = hFrac
	MOVQ BX, R15
	ANDQ $1, R15 // R15 = vFrac
	SARQ $1, AX  // AX = hInt
	SARQ $1, BX  // BX = vInt

	// Source pointer: SI = s.Y.Data + ((mbRow<<4)+vInt)*stride + (mbCol<<4)+hInt
	MOVQ  CX, DI
	SHLQ  $4, DI
	ADDQ  BX, DI // rowSrc
	MOVQ  DX, BX
	SHLQ  $4, BX
	ADDQ  AX, BX // colSrc
	MOVQ  DI, AX
	IMULQ R8, AX
	ADDQ  BX, AX
	MOVQ  40(R10), SI
	ADDQ  AX, SI

	// Dest pointer: DI = d.Y.Data + (mbRow<<4)*stride + (mbCol<<4)
	MOVQ  CX, AX
	SHLQ  $4, AX
	IMULQ R8, AX
	MOVQ  DX, BX
	SHLQ  $4, BX
	ADDQ  BX, AX
	MOVQ  40(R11), DI
	ADDQ  AX, DI

	CMPQ R14, $0
	JE   luma_hf0
	CMPQ R15, $0
	JE   luma_horiz
	JMP  luma_bilin

luma_hf0:
	CMPQ R15, $0
	JE   luma_direct
	JMP  luma_vert

luma_direct:
	MOVQ $16, R12

luma_direct_loop:
	MOVOU (SI), X0
	MOVOU X0, (DI)
	ADDQ  R8, SI
	ADDQ  R8, DI
	DECQ  R12
	JNZ   luma_direct_loop
	JMP   chroma_begin

luma_horiz:
	MOVQ $16, R12

luma_horiz_loop:
	MOVOU (SI), X0
	MOVOU 1(SI), X1
	PAVGB X1, X0
	MOVOU X0, (DI)
	ADDQ  R8, SI
	ADDQ  R8, DI
	DECQ  R12
	JNZ   luma_horiz_loop
	JMP   chroma_begin

luma_vert:
	MOVQ $16, R12

luma_vert_loop:
	MOVOU (SI), X0
	MOVOU (SI)(R8*1), X1
	PAVGB X1, X0
	MOVOU X0, (DI)
	ADDQ  R8, SI
	ADDQ  R8, DI
	DECQ  R12
	JNZ   luma_vert_loop
	JMP   chroma_begin

luma_bilin:
	MOVQ $16, R12

luma_bilin_loop:
	MOVOU (SI), X0        // a = Y[i,j]
	MOVOU 1(SI), X1       // b = Y[i,j+1]
	MOVOU (SI)(R8*1), X2  // c = Y[i+1,j]
	MOVOU 1(SI)(R8*1), X3 // d = Y[i+1,j+1]

	// Low 8 bytes: widen to words, sum a+b+c+d, +2, >>2, pack.
	MOVO      X0, X4
	PUNPCKLBW X15, X4
	MOVO      X1, X5
	PUNPCKLBW X15, X5
	PADDW     X5, X4
	MOVO      X2, X6
	PUNPCKLBW X15, X6
	MOVO      X3, X7
	PUNPCKLBW X15, X7
	PADDW     X7, X6
	PADDW     X6, X4
	PADDW     X13, X4
	PSRLW     $2, X4
	PACKUSWB  X15, X4

	// High 8 bytes.
	MOVO      X0, X8
	PUNPCKHBW X15, X8
	MOVO      X1, X9
	PUNPCKHBW X15, X9
	PADDW     X9, X8
	MOVO      X2, X10
	PUNPCKHBW X15, X10
	MOVO      X3, X11
	PUNPCKHBW X15, X11
	PADDW     X11, X10
	PADDW     X10, X8
	PADDW     X13, X8
	PSRLW     $2, X8
	PACKUSWB  X15, X8

	MOVQ X4, (DI)
	MOVQ X8, 8(DI)
	ADDQ R8, SI
	ADDQ R8, DI
	DECQ R12
	JNZ  luma_bilin_loop

	// Fall through to chroma.

chroma_begin:
	// Chroma motion is motionH/2, motionV/2 truncated toward zero. An
	// arithmetic shift truncates toward -inf, so add 1 back when the value
	// is negative and odd.
	MOVQ  motionH+0(FP), AX
	MOVQ  AX, R14
	SARQ  $1, R14
	TESTQ AX, AX
	JGE   chroma_h_ok
	TESTQ $1, AX
	JZ    chroma_h_ok
	ADDQ  $1, R14

chroma_h_ok:
	MOVQ R14, R15
	ANDQ $1, R15 // R15 = hFrac
	SARQ $1, R14 // R14 = hInt

	MOVQ  motionV+8(FP), AX
	MOVQ  AX, R13
	SARQ  $1, R13
	TESTQ AX, AX
	JGE   chroma_v_ok
	TESTQ $1, AX
	JZ    chroma_v_ok
	ADDQ  $1, R13

chroma_v_ok:
	MOVQ R13, R12
	ANDQ $1, R12 // R12 = vFrac
	SARQ $1, R13 // R13 = vInt

	// Source offset, then resolve Cb (SI) and Cr (R10) pointers.
	MOVQ  mbRow+16(FP), SI
	SHLQ  $3, SI
	ADDQ  R13, SI
	MOVQ  mbCol+24(FP), BX
	SHLQ  $3, BX
	ADDQ  R14, BX
	MOVQ  SI, AX
	IMULQ R9, AX
	ADDQ  BX, AX
	MOVQ  80(R10), SI
	ADDQ  AX, SI
	MOVQ  120(R10), R10
	ADDQ  AX, R10

	// Dest offset, then resolve Cb (DI) and Cr (R11) pointers.
	MOVQ  mbRow+16(FP), AX
	SHLQ  $3, AX
	IMULQ R9, AX
	MOVQ  mbCol+24(FP), BX
	SHLQ  $3, BX
	ADDQ  BX, AX
	MOVQ  80(R11), DI
	ADDQ  AX, DI
	MOVQ  120(R11), R11
	ADDQ  AX, R11

	MOVQ $0, R13 // plane flag: 0 = Cb (first), 1 = Cr (second)

	// Both chroma planes share this block: process the plane at (SI, DI),
	// then chroma_next swaps in the Cr pointers and runs it a second time.
chroma_plane:
	CMPQ R15, $0
	JE   chroma_hf0
	CMPQ R12, $0
	JE   chroma_horiz
	JMP  chroma_bilin

chroma_hf0:
	CMPQ R12, $0
	JE   chroma_direct
	JMP  chroma_vert

chroma_direct:
	MOVQ $8, AX

chroma_direct_loop:
	MOVQ (SI), X0
	MOVQ X0, (DI)
	ADDQ R9, SI
	ADDQ R9, DI
	DECQ AX
	JNZ  chroma_direct_loop
	JMP  chroma_next

chroma_horiz:
	MOVQ $8, AX

chroma_horiz_loop:
	MOVQ  (SI), X0
	MOVQ  1(SI), X1
	PAVGB X1, X0
	MOVQ  X0, (DI)
	ADDQ  R9, SI
	ADDQ  R9, DI
	DECQ  AX
	JNZ   chroma_horiz_loop
	JMP   chroma_next

chroma_vert:
	MOVQ $8, AX

chroma_vert_loop:
	MOVQ  (SI), X0
	MOVQ  (SI)(R9*1), X1
	PAVGB X1, X0
	MOVQ  X0, (DI)
	ADDQ  R9, SI
	ADDQ  R9, DI
	DECQ  AX
	JNZ   chroma_vert_loop
	JMP   chroma_next

chroma_bilin:
	MOVQ $8, AX

chroma_bilin_loop:
	MOVQ      (SI), X0
	MOVQ      1(SI), X1
	MOVQ      (SI)(R9*1), X2
	MOVQ      1(SI)(R9*1), X3
	PUNPCKLBW X15, X0
	PUNPCKLBW X15, X1
	PUNPCKLBW X15, X2
	PUNPCKLBW X15, X3
	PADDW     X1, X0
	PADDW     X3, X2
	PADDW     X2, X0
	PADDW     X13, X0
	PSRLW     $2, X0
	PACKUSWB  X15, X0
	MOVQ      X0, (DI)
	ADDQ      R9, SI
	ADDQ      R9, DI
	DECQ      AX
	JNZ       chroma_bilin_loop
	JMP       chroma_next

chroma_next:
	CMPQ R13, $0
	JNE  chroma_done
	MOVQ $1, R13
	MOVQ R10, SI // SI = s.Cr.Data
	MOVQ R11, DI // DI = d.Cr.Data
	JMP  chroma_plane

chroma_done:
	RET

// copyMacroblockAVX2 is the AVX2 counterpart of copyMacroblockSSE2. It uses
// VEX-encoded instructions throughout (issuing VZEROUPPER before returning),
// and widens all 16 luma words at once with VPMOVZXBW instead of unpacking two
// halves separately.
//
// func copyMacroblockAVX2(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame)
//
// Persistent registers mirror the SSE2 version; Y13/Y15 hold the word constants.
TEXT ·copyMacroblockAVX2(SB), NOSPLIT, $0-64
	MOVQ motionH+0(FP), AX
	MOVQ motionV+8(FP), BX
	MOVQ mbRow+16(FP), CX
	MOVQ mbCol+24(FP), DX
	MOVQ lumaWidth+32(FP), R8
	MOVQ chromaWidth+40(FP), R9
	MOVQ s+48(FP), R10
	MOVQ d+56(FP), R11

	VPXOR    Y15, Y15, Y15 // zero
	VPCMPEQW Y13, Y13, Y13
	VPSRLW   $15, Y13, Y13 // 0x0001 per word
	VPADDW   Y13, Y13, Y13 // 0x0002 per word

	MOVQ AX, R14
	ANDQ $1, R14 // R14 = hFrac
	MOVQ BX, R15
	ANDQ $1, R15 // R15 = vFrac
	SARQ $1, AX  // AX = hInt
	SARQ $1, BX  // BX = vInt

	MOVQ  CX, DI
	SHLQ  $4, DI
	ADDQ  BX, DI // rowSrc
	MOVQ  DX, BX
	SHLQ  $4, BX
	ADDQ  AX, BX // colSrc
	MOVQ  DI, AX
	IMULQ R8, AX
	ADDQ  BX, AX
	MOVQ  40(R10), SI
	ADDQ  AX, SI

	MOVQ  CX, AX
	SHLQ  $4, AX
	IMULQ R8, AX
	MOVQ  DX, BX
	SHLQ  $4, BX
	ADDQ  BX, AX
	MOVQ  40(R11), DI
	ADDQ  AX, DI

	CMPQ R14, $0
	JE   luma_avx2_hf0
	CMPQ R15, $0
	JE   luma_avx2_horiz
	JMP  luma_avx2_bilin

luma_avx2_hf0:
	CMPQ R15, $0
	JE   luma_avx2_direct
	JMP  luma_avx2_vert

luma_avx2_direct:
	MOVQ $16, R12

luma_avx2_direct_loop:
	VMOVDQU (SI), X0
	VMOVDQU X0, (DI)
	ADDQ    R8, SI
	ADDQ    R8, DI
	DECQ    R12
	JNZ     luma_avx2_direct_loop
	JMP     chroma_avx2_begin

luma_avx2_horiz:
	MOVQ $16, R12

luma_avx2_horiz_loop:
	VMOVDQU (SI), X0
	VMOVDQU 1(SI), X1
	VPAVGB  X1, X0, X0
	VMOVDQU X0, (DI)
	ADDQ    R8, SI
	ADDQ    R8, DI
	DECQ    R12
	JNZ     luma_avx2_horiz_loop
	JMP     chroma_avx2_begin

luma_avx2_vert:
	MOVQ $16, R12

luma_avx2_vert_loop:
	VMOVDQU (SI), X0
	VMOVDQU (SI)(R8*1), X1
	VPAVGB  X1, X0, X0
	VMOVDQU X0, (DI)
	ADDQ    R8, SI
	ADDQ    R8, DI
	DECQ    R12
	JNZ     luma_avx2_vert_loop
	JMP     chroma_avx2_begin

luma_avx2_bilin:
	MOVQ $16, R12

luma_avx2_bilin_loop:
	VMOVDQU   (SI), X0        // a
	VMOVDQU   1(SI), X1       // b
	VMOVDQU   (SI)(R8*1), X2  // c
	VMOVDQU   1(SI)(R8*1), X3 // d
	VPMOVZXBW X0, Y4
	VPMOVZXBW X1, Y5
	VPMOVZXBW X2, Y6
	VPMOVZXBW X3, Y7
	VPADDW    Y5, Y4, Y4
	VPADDW    Y7, Y6, Y6
	VPADDW    Y6, Y4, Y4
	VPADDW    Y13, Y4, Y4
	VPSRLW    $2, Y4, Y4

	// Pack 16 words back to 16 bytes across both 128-bit lanes.
	VEXTRACTI128 $1, Y4, X5
	VPACKUSWB    X5, X4, X4
	VMOVDQU      X4, (DI)

	ADDQ R8, SI
	ADDQ R8, DI
	DECQ R12
	JNZ  luma_avx2_bilin_loop

	// Fall through to chroma.

chroma_avx2_begin:
	MOVQ  motionH+0(FP), AX
	MOVQ  AX, R14
	SARQ  $1, R14
	TESTQ AX, AX
	JGE   chroma_avx2_h_ok
	TESTQ $1, AX
	JZ    chroma_avx2_h_ok
	ADDQ  $1, R14

chroma_avx2_h_ok:
	MOVQ R14, R15
	ANDQ $1, R15 // R15 = hFrac
	SARQ $1, R14 // R14 = hInt

	MOVQ  motionV+8(FP), AX
	MOVQ  AX, R13
	SARQ  $1, R13
	TESTQ AX, AX
	JGE   chroma_avx2_v_ok
	TESTQ $1, AX
	JZ    chroma_avx2_v_ok
	ADDQ  $1, R13

chroma_avx2_v_ok:
	MOVQ R13, R12
	ANDQ $1, R12 // R12 = vFrac
	SARQ $1, R13 // R13 = vInt

	MOVQ  mbRow+16(FP), SI
	SHLQ  $3, SI
	ADDQ  R13, SI
	MOVQ  mbCol+24(FP), BX
	SHLQ  $3, BX
	ADDQ  R14, BX
	MOVQ  SI, AX
	IMULQ R9, AX
	ADDQ  BX, AX
	MOVQ  80(R10), SI
	ADDQ  AX, SI
	MOVQ  120(R10), R10
	ADDQ  AX, R10

	MOVQ  mbRow+16(FP), AX
	SHLQ  $3, AX
	IMULQ R9, AX
	MOVQ  mbCol+24(FP), BX
	SHLQ  $3, BX
	ADDQ  BX, AX
	MOVQ  80(R11), DI
	ADDQ  AX, DI
	MOVQ  120(R11), R11
	ADDQ  AX, R11

	MOVQ $0, R13 // plane flag: 0 = Cb, 1 = Cr

chroma_avx2_plane:
	CMPQ R15, $0
	JE   chroma_avx2_hf0
	CMPQ R12, $0
	JE   chroma_avx2_horiz
	JMP  chroma_avx2_bilin

chroma_avx2_hf0:
	CMPQ R12, $0
	JE   chroma_avx2_direct
	JMP  chroma_avx2_vert

chroma_avx2_direct:
	MOVQ $8, AX

chroma_avx2_direct_loop:
	VMOVQ (SI), X0
	VMOVQ X0, (DI)
	ADDQ  R9, SI
	ADDQ  R9, DI
	DECQ  AX
	JNZ   chroma_avx2_direct_loop
	JMP   chroma_avx2_next

chroma_avx2_horiz:
	MOVQ $8, AX

chroma_avx2_horiz_loop:
	VMOVQ  (SI), X0
	VMOVQ  1(SI), X1
	VPAVGB X1, X0, X0
	VMOVQ  X0, (DI)
	ADDQ   R9, SI
	ADDQ   R9, DI
	DECQ   AX
	JNZ    chroma_avx2_horiz_loop
	JMP    chroma_avx2_next

chroma_avx2_vert:
	MOVQ $8, AX

chroma_avx2_vert_loop:
	VMOVQ  (SI), X0
	VMOVQ  (SI)(R9*1), X1
	VPAVGB X1, X0, X0
	VMOVQ  X0, (DI)
	ADDQ   R9, SI
	ADDQ   R9, DI
	DECQ   AX
	JNZ    chroma_avx2_vert_loop
	JMP    chroma_avx2_next

chroma_avx2_bilin:
	MOVQ $8, AX

chroma_avx2_bilin_loop:
	VMOVQ     (SI), X0
	VMOVQ     1(SI), X1
	VMOVQ     (SI)(R9*1), X2
	VMOVQ     1(SI)(R9*1), X3
	VPMOVZXBW X0, X4
	VPMOVZXBW X1, X5
	VPMOVZXBW X2, X6
	VPMOVZXBW X3, X7
	VPADDW    X5, X4, X4
	VPADDW    X7, X6, X6
	VPADDW    X6, X4, X4
	VPADDW    X13, X4, X4
	VPSRLW    $2, X4, X4
	VPACKUSWB X4, X4, X4
	VMOVQ     X4, (DI)
	ADDQ      R9, SI
	ADDQ      R9, DI
	DECQ      AX
	JNZ       chroma_avx2_bilin_loop
	JMP       chroma_avx2_next

chroma_avx2_next:
	CMPQ R13, $0
	JNE  chroma_avx2_done
	MOVQ $1, R13
	MOVQ R10, SI
	MOVQ R11, DI
	JMP  chroma_avx2_plane

chroma_avx2_done:
	VZEROUPPER
	RET
