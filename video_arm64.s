//go:build arm64 && !noasm

#include "textflag.h"

// copyMacroblockNEON implements MPEG-1 block motion compensation using ARM64
// NEON, for luma (16x16) and chroma (8x8) blocks, with direct copy, horizontal,
// vertical, and bilinear interpolation.
//
// func copyMacroblockNEON(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame)
//
// Go's arm64 assembler does not expose URHADD/UADDL/XTN, so the rounding
// averages are built from the available NEON ops:
//   - Single axis (a+b+1)>>1 = (a>>1)+(b>>1)+((a|b)&1).
//   - Bilinear (a+b+c+d+2)>>2: widen bytes to 16-bit lanes (VUSHLL/VUSHLL2),
//     sum the four taps, add 2, shift right by 2, then narrow with VUZP1.
//
// Persistent registers:
//   R4  = lumaWidth, R5 = chromaWidth (row strides)
//   R6  = source frame, R7 = dest frame
//   R10 = hFrac, R11 = vFrac
//   R12 = current source pointer, R13 = current dest pointer
//   V29 = 0x0002 per 16-bit lane (bilinear bias)
//   V30 = 0x01 per byte (carry mask for single-axis rounding)
TEXT ·copyMacroblockNEON(SB), NOSPLIT, $0-64
	MOVD motionH+0(FP), R0
	MOVD motionV+8(FP), R1
	MOVD mbRow+16(FP), R2
	MOVD mbCol+24(FP), R3
	MOVD lumaWidth+32(FP), R4
	MOVD chromaWidth+40(FP), R5
	MOVD s+48(FP), R6
	MOVD d+56(FP), R7

	MOVD $0x0101010101010101, R21
	VMOV R21, V30.D[0]
	VMOV R21, V30.D[1]
	MOVD $0x0002000200020002, R21
	VMOV R21, V29.D[0]
	VMOV R21, V29.D[1]

	// Luma: split motion into integer offset and half-pel fraction.
	ASR $1, R0, R8
	ASR $1, R1, R9
	AND $1, R0, R10
	AND $1, R1, R11

	// Source pointer = Y.Data + ((mbRow<<4)+vInt)*stride + (mbCol<<4)+hInt
	MOVD 40(R6), R12
	MOVD 40(R7), R13
	LSL  $4, R2, R14
	ADD  R9, R14, R14
	MUL  R4, R14, R14
	LSL  $4, R3, R15
	ADD  R8, R15, R15
	ADD  R14, R15, R15
	ADD  R12, R15, R12

	// Dest pointer = Y.Data + (mbRow<<4)*stride + (mbCol<<4)
	LSL $4, R2, R14
	MUL R4, R14, R14
	LSL $4, R3, R15
	ADD R14, R15, R15
	ADD R13, R15, R13

	CBZ R10, luma_no_hfrac

luma_has_hfrac:
	CBZ R11, luma_horiz
	B   luma_bilin

luma_no_hfrac:
	CBZ R11, luma_direct
	B   luma_vert

luma_direct:
	MOVD $16, R16

luma_direct_loop:
	VLD1 (R12), [V0.B16]
	VST1 [V0.B16], (R13)
	ADD  R4, R12, R12
	ADD  R4, R13, R13
	SUB  $1, R16, R16
	CBNZ R16, luma_direct_loop
	B    chroma_begin

luma_horiz:
	MOVD $16, R16

luma_horiz_loop:
	VLD1  (R12), [V0.B16]
	ADD   $1, R12, R17
	VLD1  (R17), [V1.B16]
	VUSHR $1, V0.B16, V2.B16
	VUSHR $1, V1.B16, V3.B16
	VADD  V3.B16, V2.B16, V2.B16
	VORR  V1.B16, V0.B16, V3.B16
	VAND  V30.B16, V3.B16, V3.B16
	VADD  V3.B16, V2.B16, V2.B16
	VST1  [V2.B16], (R13)
	ADD   R4, R12, R12
	ADD   R4, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, luma_horiz_loop
	B     chroma_begin

luma_vert:
	MOVD $16, R16

luma_vert_loop:
	VLD1  (R12), [V0.B16]
	ADD   R4, R12, R17
	VLD1  (R17), [V1.B16]
	VUSHR $1, V0.B16, V2.B16
	VUSHR $1, V1.B16, V3.B16
	VADD  V3.B16, V2.B16, V2.B16
	VORR  V1.B16, V0.B16, V3.B16
	VAND  V30.B16, V3.B16, V3.B16
	VADD  V3.B16, V2.B16, V2.B16
	VST1  [V2.B16], (R13)
	ADD   R4, R12, R12
	ADD   R4, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, luma_vert_loop
	B     chroma_begin

luma_bilin:
	MOVD $16, R16

luma_bilin_loop:
	VLD1 (R12), [V0.B16]  // a = Y[i,j]
	ADD  $1, R12, R17
	VLD1 (R17), [V1.B16]  // b = Y[i,j+1]
	ADD  R4, R12, R19
	VLD1 (R19), [V2.B16]  // c = Y[i+1,j]
	ADD  $1, R19, R20
	VLD1 (R20), [V3.B16]  // d = Y[i+1,j+1]

	// Columns 0..7: widen to 16-bit, sum a+b+c+d, +2, >>2.
	VUSHLL $0, V0.B8, V4.H8
	VUSHLL $0, V1.B8, V5.H8
	VADD   V5.H8, V4.H8, V4.H8
	VUSHLL $0, V2.B8, V5.H8
	VUSHLL $0, V3.B8, V6.H8
	VADD   V6.H8, V5.H8, V5.H8
	VADD   V5.H8, V4.H8, V4.H8
	VADD   V29.H8, V4.H8, V4.H8
	VUSHR  $2, V4.H8, V4.H8

	// Columns 8..15.
	VUSHLL2 $0, V0.B16, V5.H8
	VUSHLL2 $0, V1.B16, V6.H8
	VADD    V6.H8, V5.H8, V5.H8
	VUSHLL2 $0, V2.B16, V6.H8
	VUSHLL2 $0, V3.B16, V7.H8
	VADD    V7.H8, V6.H8, V6.H8
	VADD    V6.H8, V5.H8, V5.H8
	VADD    V29.H8, V5.H8, V5.H8
	VUSHR   $2, V5.H8, V5.H8

	// Narrow both halves (low byte of each 16-bit lane) into 16 bytes.
	VUZP1 V5.B16, V4.B16, V6.B16
	VST1  [V6.B16], (R13)

	ADD  R4, R12, R12
	ADD  R4, R13, R13
	SUB  $1, R16, R16
	CBNZ R16, luma_bilin_loop

	// Fall through to chroma.

chroma_begin:
	// Chroma motion = motionH/2, motionV/2 truncated toward zero. ASR floors,
	// so add 1 back for negative values (correct for both parities).
	MOVD R0, R21
	ASR  $63, R21, R22
	AND  $1, R22, R22
	ADD  R22, R21, R21
	ASR  $1, R21, R0
	MOVD R1, R21
	ASR  $63, R21, R22
	AND  $1, R22, R22
	ADD  R22, R21, R21
	ASR  $1, R21, R1

	ASR $1, R0, R8
	ASR $1, R1, R9
	AND $1, R0, R10
	AND $1, R1, R11

	// Offsets are identical for Cb and Cr; compute once.
	// srcDelta = ((mbRow<<3)+vInt)*stride + (mbCol<<3)+hInt
	LSL $3, R2, R14
	ADD R9, R14, R14
	MUL R5, R14, R14
	LSL $3, R3, R15
	ADD R8, R15, R15
	ADD R14, R15, R15

	// dstDelta = (mbRow<<3)*stride + (mbCol<<3)
	LSL $3, R2, R14
	MUL R5, R14, R14
	LSL $3, R3, R22
	ADD R22, R14, R14

	// Cb pointers (current) and Cr pointers (held for the second pass).
	MOVD 80(R6), R12
	ADD  R15, R12, R12
	MOVD 80(R7), R13
	ADD  R14, R13, R13
	MOVD 120(R6), R23
	ADD  R15, R23, R23
	MOVD 120(R7), R24
	ADD  R14, R24, R24

	MOVD $0, R21 // plane flag: 0 = Cb, 1 = Cr

	// Both chroma planes share this body: process (R12, R13), then chroma_next
	// swaps in the Cr pointers and runs it again.
chroma_plane:
	CBZ R10, chroma_no_hfrac

chroma_has_hfrac:
	CBZ R11, chroma_horiz
	B   chroma_bilin

chroma_no_hfrac:
	CBZ R11, chroma_direct
	B   chroma_vert

chroma_direct:
	MOVD $8, R16

chroma_direct_loop:
	VLD1 (R12), [V0.D1]
	VST1 [V0.D1], (R13)
	ADD  R5, R12, R12
	ADD  R5, R13, R13
	SUB  $1, R16, R16
	CBNZ R16, chroma_direct_loop
	B    chroma_next

chroma_horiz:
	MOVD $8, R16

chroma_horiz_loop:
	VLD1  (R12), [V0.D1]
	ADD   $1, R12, R17
	VLD1  (R17), [V1.D1]
	VUSHR $1, V0.B8, V2.B8
	VUSHR $1, V1.B8, V3.B8
	VADD  V3.B8, V2.B8, V2.B8
	VORR  V1.B8, V0.B8, V3.B8
	VAND  V30.B8, V3.B8, V3.B8
	VADD  V3.B8, V2.B8, V2.B8
	VST1  [V2.D1], (R13)
	ADD   R5, R12, R12
	ADD   R5, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, chroma_horiz_loop
	B     chroma_next

chroma_vert:
	MOVD $8, R16

chroma_vert_loop:
	VLD1  (R12), [V0.D1]
	ADD   R5, R12, R17
	VLD1  (R17), [V1.D1]
	VUSHR $1, V0.B8, V2.B8
	VUSHR $1, V1.B8, V3.B8
	VADD  V3.B8, V2.B8, V2.B8
	VORR  V1.B8, V0.B8, V3.B8
	VAND  V30.B8, V3.B8, V3.B8
	VADD  V3.B8, V2.B8, V2.B8
	VST1  [V2.D1], (R13)
	ADD   R5, R12, R12
	ADD   R5, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, chroma_vert_loop
	B     chroma_next

chroma_bilin:
	MOVD $8, R16

chroma_bilin_loop:
	VLD1   (R12), [V0.D1]
	ADD    $1, R12, R17
	VLD1   (R17), [V1.D1]
	ADD    R5, R12, R19
	VLD1   (R19), [V2.D1]
	ADD    $1, R19, R20
	VLD1   (R20), [V3.D1]
	VUSHLL $0, V0.B8, V4.H8
	VUSHLL $0, V1.B8, V5.H8
	VADD   V5.H8, V4.H8, V4.H8
	VUSHLL $0, V2.B8, V5.H8
	VUSHLL $0, V3.B8, V6.H8
	VADD   V6.H8, V5.H8, V5.H8
	VADD   V5.H8, V4.H8, V4.H8
	VADD   V29.H8, V4.H8, V4.H8
	VUSHR  $2, V4.H8, V4.H8
	VUZP1  V4.B16, V4.B16, V6.B16
	VST1   [V6.D1], (R13)
	ADD    R5, R12, R12
	ADD    R5, R13, R13
	SUB    $1, R16, R16
	CBNZ   R16, chroma_bilin_loop
	B      chroma_next

chroma_next:
	CBNZ R21, chroma_done
	MOVD $1, R21
	MOVD R23, R12 // src = s.Cr.Data
	MOVD R24, R13 // dst = d.Cr.Data
	B    chroma_plane

chroma_done:
	RET
