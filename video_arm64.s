//go:build arm64 && !noasm

#include "textflag.h"

#define URHADD16(Vd, Vn, Vm) WORD $(0x6E201400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define URHADD8(Vd, Vn, Vm)  WORD $(0x2E201400 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define UADDL(Vd, Vn, Vm)    WORD $(0x2E200000 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define UADDL2(Vd, Vn, Vm)   WORD $(0x6E200000 | ((Vm) << 16) | ((Vn) << 5) | (Vd))
#define RSHRN_2(Vd, Vn)      WORD $(0x0F0E8C00 | ((Vn) << 5) | (Vd))
#define RSHRN2_2(Vd, Vn)     WORD $(0x4F0E8C00 | ((Vn) << 5) | (Vd))
#define MULE(Vd, Vn, Vm, i)  WORD $(0x4F808000 | (((i) & 1) << 21) | ((Vm) << 16) | (((i) >> 1) << 11) | ((Vn) << 5) | (Vd))
#define MLAE(Vd, Vn, Vm, i)  WORD $(0x6F800000 | (((i) & 1) << 21) | ((Vm) << 16) | (((i) >> 1) << 11) | ((Vn) << 5) | (Vd))
#define MLSE(Vd, Vn, Vm, i)  WORD $(0x6F804000 | (((i) & 1) << 21) | ((Vm) << 16) | (((i) >> 1) << 11) | ((Vn) << 5) | (Vd))
#define SSHR8(Vd, Vn)        WORD $(0x4F380400 | ((Vn) << 5) | (Vd))
#define SQXTN(Vd, Vn)        WORD $(0x0E614800 | ((Vn) << 5) | (Vd))
#define SQXTN2(Vd, Vn)       WORD $(0x4E614800 | ((Vn) << 5) | (Vd))
#define SQXTUN(Vd, Vn)       WORD $(0x2E212800 | ((Vn) << 5) | (Vd))
#define SQADDH(Vd, Vn, Vm)   WORD $(0x4E600C00 | ((Vm) << 16) | ((Vn) << 5) | (Vd))

// copyMacroblockNEON implements MPEG-1 block motion compensation using ARM64
// NEON, for luma (16x16) and chroma (8x8) blocks, with direct copy, horizontal,
// vertical, and bilinear interpolation.
//
// func copyMacroblockNEON(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame, avg bool)
//
// Go's arm64 assembler does not expose URHADD, UADDL or RSHRN, so they are
// encoded with WORD:
//   - Single axis and the B average (a+b+1)>>1 are URHADD.
//   - Bilinear (a+b+c+d+2)>>2 is two UADDL, an ADD, and RSHRN #2.
//
// Persistent registers:
//   R4  = lumaWidth, R5 = chromaWidth (row strides)
//   R6  = source frame, R7 = dest frame
//   R10 = hFrac, R11 = vFrac
//   R12 = current source pointer, R13 = current dest pointer
TEXT ·copyMacroblockNEON(SB), NOSPLIT, $0-65
	MOVD  motionH+0(FP), R0
	MOVBU avg+64(FP), R25
	MOVD  motionV+8(FP), R1
	MOVD mbRow+16(FP), R2
	MOVD mbCol+24(FP), R3
	MOVD lumaWidth+32(FP), R4
	MOVD chromaWidth+40(FP), R5
	MOVD s+48(FP), R6
	MOVD d+56(FP), R7


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
	CBNZ R25, luma_direct_avg
	MOVD $16, R16

	PCALIGN $16
luma_direct_loop:
	VLD1 (R12), [V0.B16]
	VST1 [V0.B16], (R13)
	ADD  R4, R12, R12
	ADD  R4, R13, R13
	SUB  $1, R16, R16
	CBNZ R16, luma_direct_loop
	B    chroma_begin

luma_horiz:
	CBNZ R25, luma_horiz_avg
	MOVD $16, R16

	PCALIGN $16
luma_horiz_loop:
	VLD1  (R12), [V0.B16]
	ADD   $1, R12, R17
	VLD1  (R17), [V1.B16]
	URHADD16(2, 0, 1)
	VST1  [V2.B16], (R13)
	ADD   R4, R12, R12
	ADD   R4, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, luma_horiz_loop
	B     chroma_begin

luma_vert:
	CBNZ R25, luma_vert_avg
	MOVD $16, R16

	PCALIGN $16
luma_vert_loop:
	VLD1  (R12), [V0.B16]
	ADD   R4, R12, R17
	VLD1  (R17), [V1.B16]
	URHADD16(2, 0, 1)
	VST1  [V2.B16], (R13)
	ADD   R4, R12, R12
	ADD   R4, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, luma_vert_loop
	B     chroma_begin

luma_bilin:
	CBNZ R25, luma_bilin_avg
	MOVD $16, R16

	PCALIGN $16
luma_bilin_loop:
	VLD1 (R12), [V0.B16]  // a = Y[i,j]
	ADD  $1, R12, R17
	VLD1 (R17), [V1.B16]  // b = Y[i,j+1]
	ADD  R4, R12, R19
	VLD1 (R19), [V2.B16]  // c = Y[i+1,j]
	ADD  $1, R19, R20
	VLD1 (R20), [V3.B16]  // d = Y[i+1,j+1]

	UADDL(4, 0, 1)
	UADDL(5, 2, 3)
	VADD     V5.H8, V4.H8, V4.H8
	RSHRN_2(6, 4)
	UADDL2(4, 0, 1)
	UADDL2(5, 2, 3)
	VADD     V5.H8, V4.H8, V4.H8
	RSHRN2_2(6, 4)
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
	CBNZ R25, chroma_direct_avg
	MOVD $8, R16

	PCALIGN $16
chroma_direct_loop:
	VLD1 (R12), [V0.D1]
	VST1 [V0.D1], (R13)
	ADD  R5, R12, R12
	ADD  R5, R13, R13
	SUB  $1, R16, R16
	CBNZ R16, chroma_direct_loop
	B    chroma_next

chroma_horiz:
	CBNZ R25, chroma_horiz_avg
	MOVD $8, R16

	PCALIGN $16
chroma_horiz_loop:
	VLD1  (R12), [V0.D1]
	ADD   $1, R12, R17
	VLD1  (R17), [V1.D1]
	URHADD8(2, 0, 1)
	VST1  [V2.D1], (R13)
	ADD   R5, R12, R12
	ADD   R5, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, chroma_horiz_loop
	B     chroma_next

chroma_vert:
	CBNZ R25, chroma_vert_avg
	MOVD $8, R16

	PCALIGN $16
chroma_vert_loop:
	VLD1  (R12), [V0.D1]
	ADD   R5, R12, R17
	VLD1  (R17), [V1.D1]
	URHADD8(2, 0, 1)
	VST1  [V2.D1], (R13)
	ADD   R5, R12, R12
	ADD   R5, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, chroma_vert_loop
	B     chroma_next

chroma_bilin:
	CBNZ R25, chroma_bilin_avg
	MOVD $8, R16

	PCALIGN $16
chroma_bilin_loop:
	VLD1   (R12), [V0.D1]
	ADD    $1, R12, R17
	VLD1   (R17), [V1.D1]
	ADD    R5, R12, R19
	VLD1   (R19), [V2.D1]
	ADD    $1, R19, R20
	VLD1   (R20), [V3.D1]
	UADDL(4, 0, 1)
	UADDL(5, 2, 3)
	VADD   V5.H8, V4.H8, V4.H8
	RSHRN_2(6, 4)
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

luma_direct_avg:
	MOVD $16, R16

	PCALIGN $16
luma_direct_avg_loop:
	VLD1 (R12), [V0.B16]
	VLD1  (R13), [V16.B16]
	URHADD16(0, 0, 16)
	VST1 [V0.B16], (R13)
	ADD  R4, R12, R12
	ADD  R4, R13, R13
	SUB  $1, R16, R16
	CBNZ R16, luma_direct_avg_loop
	B    chroma_begin

luma_horiz_avg:
	MOVD $16, R16

	PCALIGN $16
luma_horiz_avg_loop:
	VLD1  (R12), [V0.B16]
	ADD   $1, R12, R17
	VLD1  (R17), [V1.B16]
	URHADD16(2, 0, 1)
	VLD1  (R13), [V16.B16]
	URHADD16(2, 2, 16)
	VST1  [V2.B16], (R13)
	ADD   R4, R12, R12
	ADD   R4, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, luma_horiz_avg_loop
	B    chroma_begin

luma_vert_avg:
	MOVD $16, R16

	PCALIGN $16
luma_vert_avg_loop:
	VLD1  (R12), [V0.B16]
	ADD   R4, R12, R17
	VLD1  (R17), [V1.B16]
	URHADD16(2, 0, 1)
	VLD1  (R13), [V16.B16]
	URHADD16(2, 2, 16)
	VST1  [V2.B16], (R13)
	ADD   R4, R12, R12
	ADD   R4, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, luma_vert_avg_loop
	B    chroma_begin

luma_bilin_avg:
	MOVD $16, R16

	PCALIGN $16
luma_bilin_avg_loop:
	VLD1 (R12), [V0.B16]  // a = Y[i,j]
	ADD  $1, R12, R17
	VLD1 (R17), [V1.B16]  // b = Y[i,j+1]
	ADD  R4, R12, R19
	VLD1 (R19), [V2.B16]  // c = Y[i+1,j]
	ADD  $1, R19, R20
	VLD1 (R20), [V3.B16]  // d = Y[i+1,j+1]

	UADDL(4, 0, 1)
	UADDL(5, 2, 3)
	VADD     V5.H8, V4.H8, V4.H8
	RSHRN_2(6, 4)
	UADDL2(4, 0, 1)
	UADDL2(5, 2, 3)
	VADD     V5.H8, V4.H8, V4.H8
	RSHRN2_2(6, 4)
	VLD1  (R13), [V16.B16]
	URHADD16(6, 6, 16)
	VST1  [V6.B16], (R13)

	ADD  R4, R12, R12
	ADD  R4, R13, R13
	SUB  $1, R16, R16
	CBNZ R16, luma_bilin_avg_loop
	B    chroma_begin

chroma_direct_avg:
	MOVD $8, R16

	PCALIGN $16
chroma_direct_avg_loop:
	VLD1 (R12), [V0.D1]
	VLD1  (R13), [V16.D1]
	URHADD8(0, 0, 16)
	VST1 [V0.D1], (R13)
	ADD  R5, R12, R12
	ADD  R5, R13, R13
	SUB  $1, R16, R16
	CBNZ R16, chroma_direct_avg_loop
	B    chroma_next

chroma_horiz_avg:
	MOVD $8, R16

	PCALIGN $16
chroma_horiz_avg_loop:
	VLD1  (R12), [V0.D1]
	ADD   $1, R12, R17
	VLD1  (R17), [V1.D1]
	URHADD8(2, 0, 1)
	VLD1  (R13), [V16.D1]
	URHADD8(2, 2, 16)
	VST1  [V2.D1], (R13)
	ADD   R5, R12, R12
	ADD   R5, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, chroma_horiz_avg_loop
	B    chroma_next

chroma_vert_avg:
	MOVD $8, R16

	PCALIGN $16
chroma_vert_avg_loop:
	VLD1  (R12), [V0.D1]
	ADD   R5, R12, R17
	VLD1  (R17), [V1.D1]
	URHADD8(2, 0, 1)
	VLD1  (R13), [V16.D1]
	URHADD8(2, 2, 16)
	VST1  [V2.D1], (R13)
	ADD   R5, R12, R12
	ADD   R5, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, chroma_vert_avg_loop
	B    chroma_next

chroma_bilin_avg:
	MOVD $8, R16

	PCALIGN $16
chroma_bilin_avg_loop:
	VLD1   (R12), [V0.D1]
	ADD    $1, R12, R17
	VLD1   (R17), [V1.D1]
	ADD    R5, R12, R19
	VLD1   (R19), [V2.D1]
	ADD    $1, R19, R20
	VLD1   (R20), [V3.D1]
	UADDL(4, 0, 1)
	UADDL(5, 2, 3)
	VADD   V5.H8, V4.H8, V4.H8
	RSHRN_2(6, 4)
	VLD1  (R13), [V16.D1]
	URHADD8(6, 6, 16)
	VST1   [V6.D1], (R13)
	ADD    R5, R12, R12
	ADD    R5, R13, R13
	SUB    $1, R16, R16
	CBNZ   R16, chroma_bilin_avg_loop
	B    chroma_next

// func idctNEON(block *int32, dst *uint8, stride int, add bool)
TEXT ·idctNEON(SB), NOSPLIT, $0-25
	MOVD block+0(FP), R0
	MOVD dst+8(FP), R1
	MOVD stride+16(FP), R2
	MOVBU add+24(FP), R5
	MOVD $idctconst<>(SB), R4
	MOVD R0, R3
	VLD1.P 64(R3), [V0.S4, V1.S4, V2.S4, V3.S4]
	VLD1.P 64(R3), [V4.S4, V5.S4, V6.S4, V7.S4]
	VLD1.P 64(R3), [V8.S4, V9.S4, V10.S4, V11.S4]
	VLD1.P 64(R3), [V12.S4, V13.S4, V14.S4, V15.S4]
	VLD1 (R4), [V29.S4, V30.S4, V31.S4]
	VEOR V16.B16, V16.B16, V16.B16
	VEOR V17.B16, V17.B16, V17.B16
	VEOR V18.B16, V18.B16, V18.B16
	VEOR V19.B16, V19.B16, V19.B16
	VST1.P [V16.S4, V17.S4, V18.S4, V19.S4], 64(R0)
	VST1.P [V16.S4, V17.S4, V18.S4, V19.S4], 64(R0)
	VST1.P [V16.S4, V17.S4, V18.S4, V19.S4], 64(R0)
	VST1.P [V16.S4, V17.S4, V18.S4, V19.S4], 64(R0)
	VADD V29.S4, V0.S4, V0.S4
	VADD V12.S4, V4.S4, V16.S4
	VSUB V12.S4, V4.S4, V17.S4
	VSUB V6.S4, V10.S4, V4.S4
	VADD V10.S4, V6.S4, V12.S4
	VADD V14.S4, V2.S4, V6.S4
	VSUB V14.S4, V2.S4, V10.S4
	VADD V12.S4, V6.S4, V2.S4
	VSUB V12.S4, V6.S4, V14.S4
	MULE(6, 10, 31, 0)
	MLSE(6, 4, 31, 1)
	VADD V30.S4, V6.S4, V6.S4
	SSHR8(6, 6)
	VSUB V2.S4, V6.S4, V6.S4
	MULE(14, 14, 31, 2)
	VADD V30.S4, V14.S4, V14.S4
	SSHR8(14, 14)
	VSUB V14.S4, V6.S4, V14.S4
	VSUB V8.S4, V0.S4, V12.S4
	VADD V8.S4, V0.S4, V0.S4
	MULE(17, 17, 31, 2)
	VADD V30.S4, V17.S4, V17.S4
	SSHR8(17, 17)
	VSUB V16.S4, V17.S4, V17.S4
	VADD V17.S4, V12.S4, V8.S4
	VSUB V17.S4, V12.S4, V18.S4
	VADD V16.S4, V0.S4, V12.S4
	VSUB V16.S4, V0.S4, V17.S4
	MULE(0, 4, 31, 0)
	MLAE(0, 10, 31, 1)
	VADD V30.S4, V0.S4, V0.S4
	SSHR8(0, 0)
	VADD V14.S4, V0.S4, V0.S4
	VADD V2.S4, V12.S4, V4.S4
	VSUB V2.S4, V12.S4, V10.S4
	VADD V6.S4, V8.S4, V2.S4
	VSUB V6.S4, V8.S4, V12.S4
	VSUB V14.S4, V18.S4, V6.S4
	VADD V14.S4, V18.S4, V8.S4
	VADD V0.S4, V17.S4, V14.S4
	VSUB V0.S4, V17.S4, V16.S4
	VADD V13.S4, V5.S4, V0.S4
	VSUB V13.S4, V5.S4, V17.S4
	VSUB V7.S4, V11.S4, V5.S4
	VADD V11.S4, V7.S4, V13.S4
	VADD V15.S4, V3.S4, V7.S4
	VSUB V15.S4, V3.S4, V11.S4
	VADD V13.S4, V7.S4, V3.S4
	VSUB V13.S4, V7.S4, V15.S4
	MULE(7, 11, 31, 0)
	MLSE(7, 5, 31, 1)
	VADD V30.S4, V7.S4, V7.S4
	SSHR8(7, 7)
	VSUB V3.S4, V7.S4, V7.S4
	MULE(15, 15, 31, 2)
	VADD V30.S4, V15.S4, V15.S4
	SSHR8(15, 15)
	VSUB V15.S4, V7.S4, V15.S4
	VSUB V9.S4, V1.S4, V13.S4
	VADD V9.S4, V1.S4, V1.S4
	MULE(17, 17, 31, 2)
	VADD V30.S4, V17.S4, V17.S4
	SSHR8(17, 17)
	VSUB V0.S4, V17.S4, V17.S4
	VADD V17.S4, V13.S4, V9.S4
	VSUB V17.S4, V13.S4, V18.S4
	VADD V0.S4, V1.S4, V13.S4
	VSUB V0.S4, V1.S4, V17.S4
	MULE(0, 5, 31, 0)
	MLAE(0, 11, 31, 1)
	VADD V30.S4, V0.S4, V0.S4
	SSHR8(0, 0)
	VADD V15.S4, V0.S4, V0.S4
	VADD V3.S4, V13.S4, V1.S4
	VSUB V3.S4, V13.S4, V5.S4
	VADD V7.S4, V9.S4, V3.S4
	VSUB V7.S4, V9.S4, V11.S4
	VSUB V15.S4, V18.S4, V7.S4
	VADD V15.S4, V18.S4, V9.S4
	VADD V0.S4, V17.S4, V13.S4
	VSUB V0.S4, V17.S4, V15.S4
	VTRN1 V2.S4, V4.S4, V0.S4
	VTRN2 V2.S4, V4.S4, V17.S4
	VTRN1 V14.S4, V6.S4, V18.S4
	VTRN2 V14.S4, V6.S4, V19.S4
	VTRN1 V18.D2, V0.D2, V2.D2
	VTRN1 V19.D2, V17.D2, V4.D2
	VTRN2 V18.D2, V0.D2, V6.D2
	VTRN2 V19.D2, V17.D2, V14.D2
	VTRN1 V3.S4, V1.S4, V0.S4
	VTRN2 V3.S4, V1.S4, V17.S4
	VTRN1 V13.S4, V7.S4, V18.S4
	VTRN2 V13.S4, V7.S4, V19.S4
	VTRN1 V18.D2, V0.D2, V1.D2
	VTRN1 V19.D2, V17.D2, V3.D2
	VTRN2 V18.D2, V0.D2, V7.D2
	VTRN2 V19.D2, V17.D2, V13.D2
	VTRN1 V8.S4, V16.S4, V0.S4
	VTRN2 V8.S4, V16.S4, V17.S4
	VTRN1 V10.S4, V12.S4, V18.S4
	VTRN2 V10.S4, V12.S4, V19.S4
	VTRN1 V18.D2, V0.D2, V8.D2
	VTRN1 V19.D2, V17.D2, V10.D2
	VTRN2 V18.D2, V0.D2, V12.D2
	VTRN2 V19.D2, V17.D2, V16.D2
	VTRN1 V9.S4, V15.S4, V0.S4
	VTRN2 V9.S4, V15.S4, V17.S4
	VTRN1 V5.S4, V11.S4, V18.S4
	VTRN2 V5.S4, V11.S4, V19.S4
	VTRN1 V18.D2, V0.D2, V5.D2
	VTRN1 V19.D2, V17.D2, V9.D2
	VTRN2 V18.D2, V0.D2, V11.D2
	VTRN2 V19.D2, V17.D2, V15.D2
	VADD V7.S4, V6.S4, V0.S4
	VSUB V7.S4, V6.S4, V17.S4
	VSUB V14.S4, V3.S4, V6.S4
	VADD V3.S4, V14.S4, V7.S4
	VADD V13.S4, V4.S4, V3.S4
	VSUB V13.S4, V4.S4, V14.S4
	VADD V7.S4, V3.S4, V4.S4
	VSUB V7.S4, V3.S4, V13.S4
	MULE(3, 14, 31, 0)
	MLSE(3, 6, 31, 1)
	VADD V30.S4, V3.S4, V3.S4
	SSHR8(3, 3)
	VSUB V4.S4, V3.S4, V3.S4
	MULE(13, 13, 31, 2)
	VADD V30.S4, V13.S4, V13.S4
	SSHR8(13, 13)
	VSUB V13.S4, V3.S4, V13.S4
	VSUB V1.S4, V2.S4, V7.S4
	VADD V1.S4, V2.S4, V2.S4
	MULE(17, 17, 31, 2)
	VADD V30.S4, V17.S4, V17.S4
	SSHR8(17, 17)
	VSUB V0.S4, V17.S4, V17.S4
	VADD V17.S4, V7.S4, V1.S4
	VSUB V17.S4, V7.S4, V18.S4
	VADD V0.S4, V2.S4, V7.S4
	VSUB V0.S4, V2.S4, V17.S4
	MULE(0, 6, 31, 0)
	MLAE(0, 14, 31, 1)
	VADD V30.S4, V0.S4, V0.S4
	SSHR8(0, 0)
	VADD V13.S4, V0.S4, V0.S4
	VADD V4.S4, V7.S4, V2.S4
	SSHR8(2, 2)
	VSUB V4.S4, V7.S4, V6.S4
	SSHR8(6, 6)
	VADD V3.S4, V1.S4, V4.S4
	SSHR8(4, 4)
	VSUB V3.S4, V1.S4, V7.S4
	SSHR8(7, 7)
	VSUB V13.S4, V18.S4, V1.S4
	SSHR8(1, 1)
	VADD V13.S4, V18.S4, V3.S4
	SSHR8(3, 3)
	VADD V0.S4, V17.S4, V13.S4
	SSHR8(13, 13)
	VSUB V0.S4, V17.S4, V14.S4
	SSHR8(14, 14)
	VADD V11.S4, V12.S4, V0.S4
	VSUB V11.S4, V12.S4, V17.S4
	VSUB V16.S4, V9.S4, V11.S4
	VADD V9.S4, V16.S4, V12.S4
	VADD V15.S4, V10.S4, V9.S4
	VSUB V15.S4, V10.S4, V16.S4
	VADD V12.S4, V9.S4, V10.S4
	VSUB V12.S4, V9.S4, V15.S4
	MULE(9, 16, 31, 0)
	MLSE(9, 11, 31, 1)
	VADD V30.S4, V9.S4, V9.S4
	SSHR8(9, 9)
	VSUB V10.S4, V9.S4, V9.S4
	MULE(15, 15, 31, 2)
	VADD V30.S4, V15.S4, V15.S4
	SSHR8(15, 15)
	VSUB V15.S4, V9.S4, V15.S4
	VSUB V5.S4, V8.S4, V12.S4
	VADD V5.S4, V8.S4, V8.S4
	MULE(17, 17, 31, 2)
	VADD V30.S4, V17.S4, V17.S4
	SSHR8(17, 17)
	VSUB V0.S4, V17.S4, V17.S4
	VADD V17.S4, V12.S4, V5.S4
	VSUB V17.S4, V12.S4, V18.S4
	VADD V0.S4, V8.S4, V12.S4
	VSUB V0.S4, V8.S4, V17.S4
	MULE(0, 11, 31, 0)
	MLAE(0, 16, 31, 1)
	VADD V30.S4, V0.S4, V0.S4
	SSHR8(0, 0)
	VADD V15.S4, V0.S4, V0.S4
	VADD V10.S4, V12.S4, V8.S4
	SSHR8(8, 8)
	VSUB V10.S4, V12.S4, V11.S4
	SSHR8(11, 11)
	VADD V9.S4, V5.S4, V10.S4
	SSHR8(10, 10)
	VSUB V9.S4, V5.S4, V12.S4
	SSHR8(12, 12)
	VSUB V15.S4, V18.S4, V5.S4
	SSHR8(5, 5)
	VADD V15.S4, V18.S4, V9.S4
	SSHR8(9, 9)
	VADD V0.S4, V17.S4, V15.S4
	SSHR8(15, 15)
	VSUB V0.S4, V17.S4, V16.S4
	SSHR8(16, 16)
	VTRN1 V4.S4, V2.S4, V0.S4
	VTRN2 V4.S4, V2.S4, V17.S4
	VTRN1 V13.S4, V1.S4, V18.S4
	VTRN2 V13.S4, V1.S4, V19.S4
	VTRN1 V18.D2, V0.D2, V1.D2
	VTRN1 V19.D2, V17.D2, V2.D2
	VTRN2 V18.D2, V0.D2, V4.D2
	VTRN2 V19.D2, V17.D2, V13.D2
	VTRN1 V10.S4, V8.S4, V0.S4
	VTRN2 V10.S4, V8.S4, V17.S4
	VTRN1 V15.S4, V5.S4, V18.S4
	VTRN2 V15.S4, V5.S4, V19.S4
	VTRN1 V18.D2, V0.D2, V5.D2
	VTRN1 V19.D2, V17.D2, V8.D2
	VTRN2 V18.D2, V0.D2, V10.D2
	VTRN2 V19.D2, V17.D2, V15.D2
	VTRN1 V3.S4, V14.S4, V0.S4
	VTRN2 V3.S4, V14.S4, V17.S4
	VTRN1 V6.S4, V7.S4, V18.S4
	VTRN2 V6.S4, V7.S4, V19.S4
	VTRN1 V18.D2, V0.D2, V3.D2
	VTRN1 V19.D2, V17.D2, V6.D2
	VTRN2 V18.D2, V0.D2, V7.D2
	VTRN2 V19.D2, V17.D2, V14.D2
	VTRN1 V9.S4, V16.S4, V0.S4
	VTRN2 V9.S4, V16.S4, V17.S4
	VTRN1 V11.S4, V12.S4, V18.S4
	VTRN2 V11.S4, V12.S4, V19.S4
	VTRN1 V18.D2, V0.D2, V9.D2
	VTRN1 V19.D2, V17.D2, V11.D2
	VTRN2 V18.D2, V0.D2, V12.D2
	VTRN2 V19.D2, V17.D2, V16.D2
	SQXTN(0, 1)
	SQXTN2(0, 3)
	SQXTN(1, 2)
	SQXTN2(1, 6)
	SQXTN(2, 4)
	SQXTN2(2, 7)
	SQXTN(3, 13)
	SQXTN2(3, 14)
	SQXTN(4, 5)
	SQXTN2(4, 9)
	SQXTN(5, 8)
	SQXTN2(5, 11)
	SQXTN(6, 10)
	SQXTN2(6, 12)
	SQXTN(7, 15)
	SQXTN2(7, 16)
	CBZ R5, idct_put
	MOVD R1, R6
	VLD1.P (R6)(R2), [V8.B8]
	VLD1.P (R6)(R2), [V9.B8]
	VLD1.P (R6)(R2), [V10.B8]
	VLD1.P (R6)(R2), [V11.B8]
	VLD1.P (R6)(R2), [V12.B8]
	VLD1.P (R6)(R2), [V13.B8]
	VLD1.P (R6)(R2), [V14.B8]
	VLD1.P (R6)(R2), [V15.B8]
	VUXTL V8.B8, V8.H8
	SQADDH(0, 0, 8)
	VUXTL V9.B8, V9.H8
	SQADDH(1, 1, 9)
	VUXTL V10.B8, V10.H8
	SQADDH(2, 2, 10)
	VUXTL V11.B8, V11.H8
	SQADDH(3, 3, 11)
	VUXTL V12.B8, V12.H8
	SQADDH(4, 4, 12)
	VUXTL V13.B8, V13.H8
	SQADDH(5, 5, 13)
	VUXTL V14.B8, V14.H8
	SQADDH(6, 6, 14)
	VUXTL V15.B8, V15.H8
	SQADDH(7, 7, 15)

idct_put:
	SQXTUN(0, 0)
	VST1.P [V0.B8], (R1)(R2)
	SQXTUN(1, 1)
	VST1.P [V1.B8], (R1)(R2)
	SQXTUN(2, 2)
	VST1.P [V2.B8], (R1)(R2)
	SQXTUN(3, 3)
	VST1.P [V3.B8], (R1)(R2)
	SQXTUN(4, 4)
	VST1.P [V4.B8], (R1)(R2)
	SQXTUN(5, 5)
	VST1.P [V5.B8], (R1)(R2)
	SQXTUN(6, 6)
	VST1.P [V6.B8], (R1)(R2)
	SQXTUN(7, 7)
	VST1.P [V7.B8], (R1)(R2)
	RET 

DATA idctconst<>+0(SB)/4, $128
DATA idctconst<>+4(SB)/4, $0
DATA idctconst<>+8(SB)/4, $0
DATA idctconst<>+12(SB)/4, $0
DATA idctconst<>+16(SB)/4, $128
DATA idctconst<>+20(SB)/4, $128
DATA idctconst<>+24(SB)/4, $128
DATA idctconst<>+28(SB)/4, $128
DATA idctconst<>+32(SB)/4, $473
DATA idctconst<>+36(SB)/4, $196
DATA idctconst<>+40(SB)/4, $362
DATA idctconst<>+44(SB)/4, $0
GLOBL idctconst<>(SB), RODATA|NOPTR, $48
