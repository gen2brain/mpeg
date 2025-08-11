//go:build arm64 && !noasm

#include "textflag.h"

// copyMacroblockNEON implements MPEG-1 block motion compensation using ARM64 NEON SIMD instructions.
// This function performs motion compensation for both luminance (16x16) and chrominance (8x8) blocks
// with support for direct copy, horizontal, vertical, and bilinear interpolation modes.
//
// Function signature:
// func copyMacroblockNEON(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame)
//
// Register allocation:
//   R0-R7:   Function parameters
//   R8-R11:  Motion vector integer and fractional parts
//   R12:     Source data pointer for current plane
//   R13:     Destination data pointer for current plane
//   R14-R22: Temporary registers for address calculations and loop counters
//   V0-V7:   NEON registers for loading source data and intermediate calculations
//   V30:     Constant vector of all 1s for rounding calculations.
TEXT ·copyMacroblockNEON(SB), NOSPLIT, $0-64
	// Load function parameters from stack frame into general-purpose registers
	MOVD motionH+0(FP), R0      // R0 = horizontal motion vector (signed)
	MOVD motionV+8(FP), R1      // R1 = vertical motion vector (signed)
	MOVD mbRow+16(FP), R2       // R2 = macroblock row index
	MOVD mbCol+24(FP), R3       // R3 = macroblock column index
	MOVD lumaWidth+32(FP), R4   // R4 = luminance stride (bytes per row)
	MOVD chromaWidth+40(FP), R5 // R5 = chrominance stride (bytes per row)
	MOVD s+48(FP), R6           // R6 = source frame pointer
	MOVD d+56(FP), R7           // R7 = destination frame pointer

	// Prepare a constant vector of all 1s for rounding in averaging calculations.
	// This is used to implement (a+b+1)/2 as (a>>1)+(b>>1)+((a&b)&1).
	// R18 is a reserved register on some platforms, so we use a safe temporary register like R21.
	MOVD $0x0101010101010101, R21
	VMOV R21, V30.D[0]
	VMOV R21, V30.D[1]            // V30 now contains sixteen 1s.

	// ==================================
	// LUMINANCE (Y) PLANE PROCESSING (16x16)
	// ==================================

	// Calculate luma motion vector components for half-pixel interpolation
	ASR $1, R0, R8  // R8 = hInt = motionH >> 1 (integer horizontal offset)
	ASR $1, R1, R9  // R9 = vInt = motionV >> 1 (integer vertical offset)
	AND $1, R0, R10 // R10 = hFrac = motionH & 1 (0=full pixel, 1=half pixel)
	AND $1, R1, R11 // R11 = vFrac = motionV & 1 (0=full pixel, 1=half pixel)

	// Load Y plane data pointers from Frame struct (Y.Data is at offset 40)
	MOVD 40(R6), R12 // R12 = source Y.Data base pointer
	MOVD 40(R7), R13 // R13 = destination Y.Data base pointer

	// Calculate source data pointer with motion compensation applied
	// Formula: src_ptr = Y.Data + (mbRow*16 + vInt) * lumaWidth + (mbCol*16 + hInt)
	LSL $4, R2, R14   // R14 = mbRow * 16
	ADD R9, R14, R14  // R14 += vInt
	MUL R4, R14, R14  // R14 *= lumaWidth (row offset)
	LSL $4, R3, R15   // R15 = mbCol * 16
	ADD R8, R15, R15  // R15 += hInt
	ADD R14, R15, R15 // R15 = row_offset + col_offset
	ADD R12, R15, R12 // R12 = final source Y pointer

	// Calculate destination data pointer (no motion compensation for destination)
	// Formula: dst_ptr = Y.Data + mbRow*16*lumaWidth + mbCol*16
	LSL $4, R2, R14   // R14 = mbRow * 16
	MUL R4, R14, R14  // R14 *= lumaWidth
	LSL $4, R3, R15   // R15 = mbCol * 16
	ADD R14, R15, R15 // R15 = total destination offset
	ADD R13, R15, R13 // R13 = final destination Y pointer

	// Branch to appropriate interpolation routine based on fractional motion parts
	CBZ R10, luma_no_hfrac // If hFrac == 0, check vFrac

luma_has_hfrac:
	CBZ R11, luma_horiz_only // If vFrac == 0, horizontal interpolation only
	B   luma_bilinear        // Both hFrac and vFrac set: bilinear interpolation

luma_no_hfrac:
	CBZ R11, luma_direct_copy // If vFrac == 0, direct copy (no interpolation)
	B   luma_vert_only        // Only vFrac set: vertical interpolation

	// --- Luma Direct Copy (hFrac=0, vFrac=0) ---
luma_direct_copy:
	MOVD $16, R16 // R16 = row counter for 16 rows

luma_direct_loop:
	VLD1 (R12), [V0.B16]       // Load 16 Y bytes from source
	VST1 [V0.B16], (R13)       // Store 16 Y bytes to destination
	ADD  R4, R12, R12          // source_ptr += lumaWidth
	ADD  R4, R13, R13          // dest_ptr += lumaWidth
	SUB  $1, R16, R16          // row_counter--
	CBNZ R16, luma_direct_loop // Continue if counter != 0
	B    chroma_begin          // Jump to chrominance processing

	// --- Luma Horizontal Interpolation (hFrac=1, vFrac=0) ---
luma_horiz_only:
	MOVD $16, R16 // R16 = row counter

luma_horiz_loop:
	VLD1 (R12), [V0.B16] // V0 = Y[n] (current 16 pixels)
	ADD  $1, R12, R17    // R17 = pointer to right neighbor pixels
	VLD1 (R17), [V1.B16] // V1 = Y[n+1] (right neighbor 16 pixels)

	// Perform rounding average: (a+b+1)/2 = (a>>1)+(b>>1)+(a&b&1)
	VUSHR $1, V0.B16, V2.B16      // V2 = V0 >> 1
	VUSHR $1, V1.B16, V3.B16      // V3 = V1 >> 1
	VADD  V3.B16, V2.B16, V2.B16  // V2 = (V0>>1) + (V1>>1)
	VAND  V1.B16, V0.B16, V3.B16  // V3 = V0 & V1
	VAND  V30.B16, V3.B16, V3.B16 // V3 = (V0 & V1) & 1
	VADD  V3.B16, V2.B16, V2.B16  // V2 = V2 + V3 (final result)
	VST1  [V2.B16], (R13)         // Store 16 interpolated bytes
	ADD   R4, R12, R12
	ADD   R4, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, luma_horiz_loop
	B     chroma_begin

	// --- Luma Vertical Interpolation (hFrac=0, vFrac=1) ---
luma_vert_only:
	MOVD $16, R16 // R16 = row counter

luma_vert_loop:
	VLD1 (R12), [V0.B16] // V0 = current row
	ADD  R4, R12, R17    // R17 = pointer to next row
	VLD1 (R17), [V1.B16] // V1 = next row

	// Perform rounding average
	VUSHR $1, V0.B16, V2.B16
	VUSHR $1, V1.B16, V3.B16
	VADD  V3.B16, V2.B16, V2.B16
	VAND  V1.B16, V0.B16, V3.B16
	VAND  V30.B16, V3.B16, V3.B16
	VADD  V3.B16, V2.B16, V2.B16
	VST1  [V2.B16], (R13)
	ADD   R4, R12, R12
	ADD   R4, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, luma_vert_loop
	B     chroma_begin

	// --- Luma Bilinear Interpolation (hFrac=1, vFrac=1) ---
luma_bilinear:
	MOVD $16, R16 // R16 = row counter

luma_bilin_loop:
	VLD1 (R12), [V0.B16] // V0 = Y[i,j] (current)
	ADD  $1, R12, R17
	VLD1 (R17), [V1.B16] // V1 = Y[i,j+1] (right)
	ADD  R4, R12, R19
	VLD1 (R19), [V2.B16] // V2 = Y[i+1,j] (below)
	ADD  $1, R19, R20
	VLD1 (R20), [V3.B16] // V3 = Y[i+1,j+1] (below-right)

	// Stage 1: Horizontal averaging
	VUSHR $1, V0.B16, V4.B16
	VUSHR $1, V1.B16, V5.B16
	VADD  V5.B16, V4.B16, V4.B16
	VAND  V1.B16, V0.B16, V5.B16
	VAND  V30.B16, V5.B16, V5.B16
	VADD  V5.B16, V4.B16, V4.B16  // V4 = avg(V0, V1)
	VUSHR $1, V2.B16, V5.B16
	VUSHR $1, V3.B16, V6.B16
	VADD  V6.B16, V5.B16, V5.B16
	VAND  V3.B16, V2.B16, V6.B16
	VAND  V30.B16, V6.B16, V6.B16
	VADD  V6.B16, V5.B16, V5.B16  // V5 = avg(V2, V3)

	// Stage 2: Vertical averaging of horizontal results
	VUSHR $1, V4.B16, V6.B16
	VUSHR $1, V5.B16, V7.B16
	VADD  V7.B16, V6.B16, V6.B16
	VAND  V5.B16, V4.B16, V7.B16
	VAND  V30.B16, V7.B16, V7.B16
	VADD  V7.B16, V6.B16, V6.B16  // V6 = avg(V4, V5)
	VST1  [V6.B16], (R13)
	ADD   R4, R12, R12
	ADD   R4, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, luma_bilin_loop

	// Fall through to chrominance processing

	// ==================================
	// CHROMINANCE PROCESSING (8x8 Cb and Cr)
	// ==================================
chroma_begin:
	// The formula (val + (val < 0 ? 1 : 0)) >> 1 is implemented without branches.
	// For motionH (original in R0):
	MOVD R0, R21       // temp_h = original motionH (use safe temp R21)
	ASR  $63, R21, R22 // R22 = sign mask (-1 if negative, 0 if positive)
	AND  $1, R22, R22  // R22 = rounding constant (1 if negative, 0 if positive)
	ADD  R22, R21, R21 // Add rounding constant
	ASR  $1, R21, R0   // R0 = final chromaH = motionH/2 (rounded to zero)

	// For motionV (original in R1):
	MOVD R1, R21       // temp_v = original motionV (use safe temp R21)
	ASR  $63, R21, R22 // R22 = sign mask
	AND  $1, R22, R22  // R22 = rounding constant
	ADD  R22, R21, R21 // Add rounding constant
	ASR  $1, R21, R1   // R1 = final chromaV = motionV/2 (rounded to zero)

	// Calculate chroma fractional and integer parts from the corrected vectors
	ASR $1, R0, R8  // R8 = chromaH_int
	ASR $1, R1, R9  // R9 = chromaV_int
	AND $1, R0, R10 // R10 = chromaH_frac
	AND $1, R1, R11 // R11 = chromaV_frac

	// --- Process Cb plane (8x8 block) ---
	MOVD 80(R6), R12 // R12 = source Cb.Data base pointer (offset 80)
	MOVD 80(R7), R13 // R13 = destination Cb.Data base pointer

	LSL $3, R2, R14   // R14 = mbRow * 8
	ADD R9, R14, R14  // R14 += chromaV_int
	MUL R5, R14, R14  // R14 *= chromaWidth
	LSL $3, R3, R15   // R15 = mbCol * 8
	ADD R8, R15, R15  // R15 += chromaH_int
	ADD R14, R15, R15
	ADD R12, R15, R12 // R12 = final source Cb pointer

	LSL $3, R2, R14
	MUL R5, R14, R14
	LSL $3, R3, R15
	ADD R14, R15, R15
	ADD R13, R15, R13 // R13 = final destination Cb pointer

	CBZ R10, chroma_cb_no_hfrac

chroma_cb_has_hfrac:
	CBZ R11, chroma_cb_horiz
	B   chroma_cb_bilinear

chroma_cb_no_hfrac:
	CBZ R11, chroma_cb_direct
	B   chroma_cb_vert

chroma_cb_direct:
	MOVD $8, R16

chroma_cb_direct_loop:
	VLD1 (R12), [V0.D1]
	VST1 [V0.D1], (R13)
	ADD  R5, R12, R12
	ADD  R5, R13, R13
	SUB  $1, R16, R16
	CBNZ R16, chroma_cb_direct_loop
	B    chroma_cr_begin

chroma_cb_horiz:
	MOVD $8, R16

chroma_cb_horiz_loop:
	VLD1  (R12), [V0.D1]
	ADD   $1, R12, R17
	VLD1  (R17), [V1.D1]
	VUSHR $1, V0.B8, V2.B8
	VUSHR $1, V1.B8, V3.B8
	VADD  V3.B8, V2.B8, V2.B8
	VAND  V1.B8, V0.B8, V3.B8
	VAND  V30.B8, V3.B8, V3.B8
	VADD  V3.B8, V2.B8, V2.B8
	VST1  [V2.D1], (R13)
	ADD   R5, R12, R12
	ADD   R5, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, chroma_cb_horiz_loop
	B     chroma_cr_begin

chroma_cb_vert:
	MOVD $8, R16

chroma_cb_vert_loop:
	VLD1  (R12), [V0.D1]
	ADD   R5, R12, R17
	VLD1  (R17), [V1.D1]
	VUSHR $1, V0.B8, V2.B8
	VUSHR $1, V1.B8, V3.B8
	VADD  V3.B8, V2.B8, V2.B8
	VAND  V1.B8, V0.B8, V3.B8
	VAND  V30.B8, V3.B8, V3.B8
	VADD  V3.B8, V2.B8, V2.B8
	VST1  [V2.D1], (R13)
	ADD   R5, R12, R12
	ADD   R5, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, chroma_cb_vert_loop
	B     chroma_cr_begin

chroma_cb_bilinear:
	MOVD $8, R16

chroma_cb_bilin_loop:
	VLD1  (R12), [V0.D1]
	ADD   $1, R12, R17
	VLD1  (R17), [V1.D1]
	ADD   R5, R12, R19
	VLD1  (R19), [V2.D1]
	ADD   $1, R19, R20
	VLD1  (R20), [V3.D1]
	VUSHR $1, V0.B8, V4.B8
	VUSHR $1, V1.B8, V5.B8
	VADD  V5.B8, V4.B8, V4.B8
	VAND  V1.B8, V0.B8, V5.B8
	VAND  V30.B8, V5.B8, V5.B8
	VADD  V5.B8, V4.B8, V4.B8
	VUSHR $1, V2.B8, V5.B8
	VUSHR $1, V3.B8, V6.B8
	VADD  V6.B8, V5.B8, V5.B8
	VAND  V3.B8, V2.B8, V6.B8
	VAND  V30.B8, V6.B8, V6.B8
	VADD  V6.B8, V5.B8, V5.B8
	VUSHR $1, V4.B8, V6.B8
	VUSHR $1, V5.B8, V7.B8
	VADD  V7.B8, V6.B8, V6.B8
	VAND  V5.B8, V4.B8, V7.B8
	VAND  V30.B8, V7.B8, V7.B8
	VADD  V7.B8, V6.B8, V6.B8
	VST1  [V6.D1], (R13)
	ADD   R5, R12, R12
	ADD   R5, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, chroma_cb_bilin_loop

	// Fall through

	// --- Process Cr plane (8x8 block) ---
chroma_cr_begin:
	MOVD 120(R6), R12 // R12 = source Cr.Data base pointer (offset 120)
	MOVD 120(R7), R13 // R13 = destination Cr.Data base pointer

	LSL $3, R2, R14
	ADD R9, R14, R14
	MUL R5, R14, R14
	LSL $3, R3, R15
	ADD R8, R15, R15
	ADD R14, R15, R15
	ADD R12, R15, R12 // R12 = final source Cr pointer

	LSL $3, R2, R14
	MUL R5, R14, R14
	LSL $3, R3, R15
	ADD R14, R15, R15
	ADD R13, R15, R13 // R13 = final destination Cr pointer

	CBZ R10, chroma_cr_no_hfrac

chroma_cr_has_hfrac:
	CBZ R11, chroma_cr_horiz
	B   chroma_cr_bilinear

chroma_cr_no_hfrac:
	CBZ R11, chroma_cr_direct
	B   chroma_cr_vert

chroma_cr_direct:
	MOVD $8, R16

chroma_cr_direct_loop:
	VLD1 (R12), [V0.D1]
	VST1 [V0.D1], (R13)
	ADD  R5, R12, R12
	ADD  R5, R13, R13
	SUB  $1, R16, R16
	CBNZ R16, chroma_cr_direct_loop
	RET

chroma_cr_horiz:
	MOVD $8, R16

chroma_cr_horiz_loop:
	VLD1  (R12), [V0.D1]
	ADD   $1, R12, R17
	VLD1  (R17), [V1.D1]
	VUSHR $1, V0.B8, V2.B8
	VUSHR $1, V1.B8, V3.B8
	VADD  V3.B8, V2.B8, V2.B8
	VAND  V1.B8, V0.B8, V3.B8
	VAND  V30.B8, V3.B8, V3.B8
	VADD  V3.B8, V2.B8, V2.B8
	VST1  [V2.D1], (R13)
	ADD   R5, R12, R12
	ADD   R5, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, chroma_cr_horiz_loop
	RET

chroma_cr_vert:
	MOVD $8, R16

chroma_cr_vert_loop:
	VLD1  (R12), [V0.D1]
	ADD   R5, R12, R17
	VLD1  (R17), [V1.D1]
	VUSHR $1, V0.B8, V2.B8
	VUSHR $1, V1.B8, V3.B8
	VADD  V3.B8, V2.B8, V2.B8
	VAND  V1.B8, V0.B8, V3.B8
	VAND  V30.B8, V3.B8, V3.B8
	VADD  V3.B8, V2.B8, V2.B8
	VST1  [V2.D1], (R13)
	ADD   R5, R12, R12
	ADD   R5, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, chroma_cr_vert_loop
	RET

chroma_cr_bilinear:
	MOVD $8, R16

chroma_cr_bilin_loop:
	VLD1  (R12), [V0.D1]
	ADD   $1, R12, R17
	VLD1  (R17), [V1.D1]
	ADD   R5, R12, R19
	VLD1  (R19), [V2.D1]
	ADD   $1, R19, R20
	VLD1  (R20), [V3.D1]
	VUSHR $1, V0.B8, V4.B8
	VUSHR $1, V1.B8, V5.B8
	VADD  V5.B8, V4.B8, V4.B8
	VAND  V1.B8, V0.B8, V5.B8
	VAND  V30.B8, V5.B8, V5.B8
	VADD  V5.B8, V4.B8, V4.B8
	VUSHR $1, V2.B8, V5.B8
	VUSHR $1, V3.B8, V6.B8
	VADD  V6.B8, V5.B8, V5.B8
	VAND  V3.B8, V2.B8, V6.B8
	VAND  V30.B8, V6.B8, V6.B8
	VADD  V6.B8, V5.B8, V5.B8
	VUSHR $1, V4.B8, V6.B8
	VUSHR $1, V5.B8, V7.B8
	VADD  V7.B8, V6.B8, V6.B8
	VAND  V5.B8, V4.B8, V7.B8
	VAND  V30.B8, V7.B8, V7.B8
	VADD  V7.B8, V6.B8, V6.B8
	VST1  [V6.D1], (R13)
	ADD   R5, R12, R12
	ADD   R5, R13, R13
	SUB   $1, R16, R16
	CBNZ  R16, chroma_cr_bilin_loop
	RET

