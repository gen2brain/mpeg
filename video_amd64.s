//go:build amd64 && !noasm

#include "textflag.h"

// copyMacroblockSSE2 implements MPEG-1 block motion compensation using SSE2.
// It supports direct copy, horizontal, vertical, and bilinear interpolation
// for luma (16x16) and chroma (8x8) blocks, with rounding as per MPEG spec.
//
// Function signature:
// func copyMacroblockNEON(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame)
//
// Registers used:
//   R8  = lumaWidth                         // Holds luma stride
//   R9  = chromaWidth                       // Holds chroma stride
//   R10 = *Frame s                          // Source frame pointer
//   R11 = *Frame d                          // Destination frame pointer
//   R12, R13, R14, R15  = temporaries       // Used for calculations
//
// SSE2 registers X0-X15 are used for SIMD ops.
//   X15: all zeros (for unpacking/packing)
//   X14: all words 1 (for rounding in linear interp)
//   X13: all words 2 (for rounding in bilinear interp)
TEXT ·copyMacroblockSSE2(SB), NOSPLIT, $0-64

	// -- Load arguments from stack --
	// These are the function parameters passed via stack in Go's ABI.
	MOVQ motionH+0(FP), AX      // motionH (horizontal) into AX
	MOVQ motionV+8(FP), BX      // motionV (vertical) into BX
	MOVQ mbRow+16(FP), CX       // mbRow into CX
	MOVQ mbCol+24(FP), DX       // mbCol into DX
	MOVQ lumaWidth+32(FP), R8   // lumaWidth (stride) into R8
	MOVQ chromaWidth+40(FP), R9 // chromaWidth (stride) into R9
	MOVQ s+48(FP), R10          // *Frame s (source) into R10
	MOVQ d+56(FP), R11          // *Frame d (dest) into R11

	// -- Prepare rounding constants for SIMD --
	// X15: all zero (used for unpack/pack). PXOR clears X15 to zero.
	PXOR X15, X15

	// X14: all words set to 1 (for linear interp rounding)
	// PCMPEQW sets all words to -1 (0xFFFF), then PSRLW $15 shifts right by 15, making 0x0001 per word.
	PCMPEQW X14, X14
	PSRLW   $15, X14 // X14 = 0x0001 per word

	// X13: all words set to 2 (for bilinear interp rounding)
	// Copy X14 (all 1s) to X13, then PADDW doubles it to all 2s.
	MOVO  X14, X13
	PADDW X13, X13 // X13 = 0x0002 per word

	// -- Compute luma source offsets --
	// luma: Y.Data + yoff*stride + xoff

	// Integer + fractional parts of motion vectors
	// R12 = hInt = motionH >> 1 (arithmetic shift preserves sign)
	MOVQ AX, R12
	SARQ $1, R12 // R12 = hInt = motionH >> 1
	MOVQ BX, R13
	SARQ $1, R13 // R13 = vInt = motionV >> 1
	MOVQ AX, R14
	ANDQ $1, R14 // R14 = hFrac (0 or 1)
	MOVQ BX, R15
	ANDQ $1, R15 // R15 = vFrac (0 or 1)

	// Compute luma source pixel index (row,col), then offset
	// rowSrc = (mbRow<<4) + vInt
	MOVQ CX, DI
	SHLQ $4, DI
	ADDQ R13, DI // DI = rowSrc

	// colSrc = (mbCol<<4) + hInt
	MOVQ DX, BP
	SHLQ $4, BP
	ADDQ R12, BP // BP = colSrc

	// SI = Y.Data pointer (from Frame)
	// In Frame struct, Y.Data is at offset 40 (assuming standard layout).
	MOVQ 40(R10), SI // struct Frame, Y.Data is at offset 40

	// Compute offset: SI += rowSrc*lumaWidth + colSrc
	// temp = rowSrc * lumaWidth
	MOVQ  DI, AX
	IMULQ R8, AX
	ADDQ  BP, AX
	ADDQ  AX, SI // SI now points to source luma data with motion offset

	// Compute destination luma offset (no motion compensation for dest)
	// rowDst = mbRow<<4, colDst = mbCol<<4
	MOVQ CX, DI
	SHLQ $4, DI
	MOVQ DX, BP
	SHLQ $4, BP
	MOVQ 40(R11), DI // d.Y.Data

	// temp = rowDst * lumaWidth
	MOVQ  CX, AX
	SHLQ  $4, AX
	IMULQ R8, AX
	ADDQ  BP, AX
	ADDQ  AX, DI // DI = dest Y pointer

	// -- Luma interpolation branching --
	// Based on fractional parts (hFrac, vFrac), jump to appropriate interpolation method.
	CMPQ R14, $0
	JE   lumahf0       // hFrac == 0?
	CMPQ R15, $0
	JE   lumah1vf0     // hFrac==1, vFrac==0: horizontal interpolation
	JMP  luma_bilinear // hFrac==1, vFrac==1: bilinear

lumahf0:
	CMPQ R15, $0
	JE   luma_direct   // hFrac==0, vFrac==0: direct copy
	JMP  luma_vertical // hFrac==0, vFrac==1: vertical

// -- LUMA: Direct copy (no interpolation) --
luma_direct:
	MOVQ $16, R12 // Loop over 16 rows (luma is 16x16)

luma_direct_loop:
	// Load 16 bytes from source using MOVOU (unaligned load).
	MOVOU (SI), X0         // Load 16 Y bytes from src (full 128 bits)
	MOVOU X0, (DI)         // Store to dest (unaligned store)
	ADDQ  R8, SI           // src += stride
	ADDQ  R8, DI           // dst += stride
	DECQ  R12              // Decrement row counter
	JNZ   luma_direct_loop // Loop if not zero
	JMP   chroma_begin     // Proceed to chroma

// -- LUMA: Horizontal interpolation (average with right neighbor) --
lumah1vf0:
	MOVQ $16, R12 // 16 rows

luma_horiz_loop:
	// Load current 16 bytes and next 16 bytes (shifted by 1 for right neighbors).
	MOVOU (SI), X0  // Load 16 bytes Y[n] (full 128 bits)
	MOVOU 1(SI), X1 // Load next 16 bytes Y[n+1] (full 128 bits)

	// Process low 8 bytes:
	// Unpack bytes to words for addition (to avoid overflow).
	// PUNPCKLBW interleaves low 8 bytes with zeros (X15=0), effectively zero-extending bytes to words.
	MOVO      X0, X2
	PUNPCKLBW X15, X2 // Interleave low 8 bytes of X0 with zeros (X15=0), effectively zero-extending bytes to words
	MOVO      X1, X3
	PUNPCKLBW X15, X3 // Same for low 8 bytes of X1

	// Sum, add rounding, divide by 2
	// PADDW adds corresponding words.
	PADDW X3, X2  // Add corresponding words: average preparation
	PADDW X14, X2 // Add 1 to each word for rounding (X14 = all 1s in words)
	PSRLW $1, X2  // Arithmetic right shift by 1: divide by 2 (since words are unsigned after unpack)

	// PACKUSWB packs 16 words to 8 bytes with unsigned saturation, high 64 bits from X15 (zeros).
	PACKUSWB X15, X2 // Pack 8 words back to 8 unsigned bytes with saturation, high 64 bits from X15 (0)

	// High 8 bytes (similar process)
	MOVO      X0, X4
	PUNPCKHBW X15, X4 // Interleave high 8 bytes of X0 with zeros
	MOVO      X1, X5
	PUNPCKHBW X15, X5 // Same for high 8 bytes of X1
	PADDW     X5, X4
	PADDW     X14, X4
	PSRLW     $1, X4
	PACKUSWB  X15, X4 // Pack high 8 words to high 8 bytes

	// Store low/high using MOVQ (64-bit moves).
	MOVQ X2, (DI)        // Store low 8 bytes
	MOVQ X4, 8(DI)       // Store high 8 bytes
	ADDQ R8, SI          // Advance source
	ADDQ R8, DI          // Advance dest
	DECQ R12             // Decrement counter
	JNZ  luma_horiz_loop // Loop
	JMP  chroma_begin    // To chroma

// -- LUMA: Vertical interpolation (average with row below) --
luma_vertical:
	MOVQ $16, R12 // 16 rows

luma_vert_loop:
	// Load current row and next row.
	MOVOU (SI), X0
	MOVOU (SI)(R8*1), X1 // Next row (stride R8)

	// Unpack low 8
	MOVO      X0, X2
	PUNPCKLBW X15, X2 // Unpack low 8 bytes to words
	MOVO      X1, X3
	PUNPCKLBW X15, X3 // Unpack low 8 of next row
	PADDW     X3, X2  // Add
	PADDW     X14, X2 // Round
	PSRLW     $1, X2  // Divide by 2
	PACKUSWB  X15, X2 // Pack to bytes

	// Unpack high 8
	MOVO      X0, X4
	PUNPCKHBW X15, X4 // Unpack high 8
	MOVO      X1, X5
	PUNPCKHBW X15, X5
	PADDW     X5, X4
	PADDW     X14, X4
	PSRLW     $1, X4
	PACKUSWB  X15, X4

	// Store
	MOVQ X2, (DI)
	MOVQ X4, 8(DI)
	ADDQ R8, SI
	ADDQ R8, DI
	DECQ R12
	JNZ  luma_vert_loop
	JMP  chroma_begin

// -- LUMA: Bilinear interpolation (average 4 neighbors) --
luma_bilinear:
	MOVQ $16, R12 // 16 rows

luma_bilin_loop:
	// Load four neighboring rows: current, right, below, below-right.
	MOVOU (SI), X0        // Y[i,j] (16 bytes)
	MOVOU 1(SI), X1       // Y[i,j+1]
	MOVOU (SI)(R8*1), X2  // Y[i+1,j]
	MOVOU 1(SI)(R8*1), X3 // Y[i+1,j+1]

	// Low 8 bytes:
	// Unpack each to words.
	MOVO      X0, X4
	PUNPCKLBW X15, X4 // Zero-extend low 8 bytes to words
	MOVO      X1, X5
	PUNPCKLBW X15, X5
	PADDW     X5, X4  // Sum horizontal pair
	MOVO      X2, X6
	PUNPCKLBW X15, X6
	MOVO      X3, X7
	PUNPCKLBW X15, X7
	PADDW     X7, X6  // Sum horizontal pair below
	PADDW     X6, X4  // Sum vertical
	PADDW     X13, X4 // Add 2 for rounding (X13 = all 2s)
	PSRLW     $2, X4  // Divide by 4 (shift right by 2)
	PACKUSWB  X15, X4 // Pack to bytes

	// High 8 bytes (similar):
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

	// Store
	MOVQ X4, (DI)
	MOVQ X8, 8(DI)
	ADDQ R8, SI
	ADDQ R8, DI
	DECQ R12
	JNZ  luma_bilin_loop

	// -- Done with luma, do chroma --
	// Fallthrough

	// -- Compute chroma source offsets (Cb/Cr are half res of luma) --
chroma_begin:
	// Chroma motion vectors: (motionH/2), (motionV/2)
	// Truncate toward zero for signed division
	MOVQ motionH+0(FP), R12
	SARQ $1, R12            // chromaH = motionH >> 1 (arithmetic shift)
	MOVQ motionV+8(FP), R13
	SARQ $1, R13            // chromaV = motionV >> 1 (arithmetic shift)

	// Adjust for toward zero truncation if negative and odd
	// For negative values, SAR truncates toward negative infinity, but we want toward zero.
	MOVQ  motionH+0(FP), AX
	TESTQ AX, AX            // Check if motionH negative
	JGE   chroma_h_zero
	TESTQ $1, AX            // Check if odd
	JZ    chroma_h_zero
	ADDQ  $1, R12           // If negative and odd, add 1 to make less negative (toward zero)

chroma_h_zero:
	MOVQ R12, R14
	SARQ $1, R14  // chromaH_int = chromaH >> 1
	MOVQ R12, R15
	ANDQ $1, R15  // chromaH_frac = chromaH & 1

	// chromaH_int = R14, chromaH_frac = R15

	MOVQ  motionV+8(FP), AX
	TESTQ AX, AX
	JGE   chroma_v_zero
	TESTQ $1, AX
	JZ    chroma_v_zero
	ADDQ  $1, R13

chroma_v_zero:
	MOVQ R13, BX  // chromaV_int = BX
	SARQ $1, BX   // chromaV_int = chromaV >> 1
	MOVQ R13, R12 // chromaV_frac = R13 & 1, but use R12 to avoid conflict with DI
	ANDQ $1, R12

	// chromaV_int = BX, chromaV_frac = R12 (changed from DI to R12 to avoid register conflict)

	// Compute Cb/Cr source and dest pointers
	// src row = (mbRow<<3) + chromaV_int
	MOVQ mbRow+16(FP), SI
	SHLQ $3, SI
	ADDQ BX, SI

	// src col = (mbCol<<3) + chromaH_int
	MOVQ mbCol+24(FP), BP
	SHLQ $3, BP
	ADDQ R14, BP

	// src offset = src_row*chromaWidth + src_col
	MOVQ  SI, AX
	IMULQ R9, AX
	ADDQ  BP, AX

	// Source pointers
	// Cb.Data at offset 80, Cr.Data at 120 in Frame struct.
	MOVQ 80(R10), SI   // s.Cb.Data
	ADDQ AX, SI
	MOVQ 120(R10), R10 // s.Cr.Data
	ADDQ AX, R10

	// dest row = mbRow<<3
	MOVQ mbRow+16(FP), CX
	SHLQ $3, CX

	// dest col = mbCol<<3
	MOVQ mbCol+24(FP), BP
	SHLQ $3, BP

	// dest offset = dest_row*chromaWidth + dest_col
	MOVQ  CX, AX
	IMULQ R9, AX
	ADDQ  BP, AX

	// Dest pointers
	MOVQ 80(R11), DI   // d.Cb.Data
	ADDQ AX, DI
	MOVQ 120(R11), R11 // d.Cr.Data
	ADDQ AX, R11

	// -- Chroma interpolation branching for Cb --
	// Similar branching as luma, but for 8x8 blocks.
	CMPQ R15, $0
	JE   chroma_cb_hf0
	CMPQ R12, $0
	JE   chroma_cb_h1vf0
	JMP  chroma_cb_bilinear

chroma_cb_hf0:
	CMPQ R12, $0
	JE   chroma_cb_direct
	JMP  chroma_cb_vertical

chroma_cb_direct:
	MOVQ $8, AX // 8 rows for chroma

chroma_cb_direct_loop:
	// Direct copy 8 bytes using MOVQ (64-bit load/store).
	MOVQ (SI), X0
	MOVQ X0, (DI)
	ADDQ R9, SI
	ADDQ R9, DI
	DECQ AX
	JNZ  chroma_cb_direct_loop
	JMP  chroma_cr_begin

chroma_cb_h1vf0:
	MOVQ $8, AX

chroma_cb_horiz_loop:
	// Load 8 bytes and next 8 (shifted by 1).
	MOVQ (SI), X0
	MOVQ 1(SI), X1

	// Unpack to words (PUNPCKLBW with X15=0).
	PUNPCKLBW X15, X0
	PUNPCKLBW X15, X1
	PADDW     X1, X0
	PADDW     X14, X0              // Round
	PSRLW     $1, X0               // Divide
	PACKUSWB  X15, X0              // Pack
	MOVQ      X0, (DI)
	ADDQ      R9, SI
	ADDQ      R9, DI
	DECQ      AX
	JNZ       chroma_cb_horiz_loop
	JMP       chroma_cr_begin

chroma_cb_vertical:
	MOVQ $8, AX

chroma_cb_vert_loop:
	MOVQ      (SI), X0
	MOVQ      (SI)(R9*1), X1
	PUNPCKLBW X15, X0
	PUNPCKLBW X15, X1
	PADDW     X1, X0
	PADDW     X14, X0
	PSRLW     $1, X0
	PACKUSWB  X15, X0
	MOVQ      X0, (DI)
	ADDQ      R9, SI
	ADDQ      R9, DI
	DECQ      AX
	JNZ       chroma_cb_vert_loop
	JMP       chroma_cr_begin

chroma_cb_bilinear:
	MOVQ $8, AX

chroma_cb_bilin_loop:
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
	PADDW     X13, X0              // Round by 2
	PSRLW     $2, X0               // Divide by 4
	PACKUSWB  X15, X0
	MOVQ      X0, (DI)
	ADDQ      R9, SI
	ADDQ      R9, DI
	DECQ      AX
	JNZ       chroma_cb_bilin_loop
	JMP       chroma_cr_begin

// -- Chroma Cr plane --
chroma_cr_begin:
	// R10: s.Cr.Data, R11: d.Cr.Data
	// Use same chroma motion as above (R15: hFrac, R12: vFrac)
	// Branching similar to Cb.
	CMPQ R15, $0
	JE   chroma_cr_hf0
	CMPQ R12, $0
	JE   chroma_cr_h1vf0
	JMP  chroma_cr_bilinear

chroma_cr_hf0:
	CMPQ R12, $0
	JE   chroma_cr_direct
	JMP  chroma_cr_vertical

chroma_cr_direct:
	MOVQ $8, AX

chroma_cr_direct_loop:
	MOVQ (R10), X0
	MOVQ X0, (R11)
	ADDQ R9, R10
	ADDQ R9, R11
	DECQ AX
	JNZ  chroma_cr_direct_loop
	RET                        // End of function

chroma_cr_h1vf0:
	MOVQ $8, AX

chroma_cr_horiz_loop:
	MOVQ      (R10), X0
	MOVQ      1(R10), X1
	PUNPCKLBW X15, X0
	PUNPCKLBW X15, X1
	PADDW     X1, X0
	PADDW     X14, X0
	PSRLW     $1, X0
	PACKUSWB  X15, X0
	MOVQ      X0, (R11)
	ADDQ      R9, R10
	ADDQ      R9, R11
	DECQ      AX
	JNZ       chroma_cr_horiz_loop
	RET

chroma_cr_vertical:
	MOVQ $8, AX

chroma_cr_vert_loop:
	MOVQ      (R10), X0
	MOVQ      (R10)(R9*1), X1
	PUNPCKLBW X15, X0
	PUNPCKLBW X15, X1
	PADDW     X1, X0
	PADDW     X14, X0
	PSRLW     $1, X0
	PACKUSWB  X15, X0
	MOVQ      X0, (R11)
	ADDQ      R9, R10
	ADDQ      R9, R11
	DECQ      AX
	JNZ       chroma_cr_vert_loop
	RET

chroma_cr_bilinear:
	MOVQ $8, AX

chroma_cr_bilin_loop:
	MOVQ      (R10), X0
	MOVQ      1(R10), X1
	MOVQ      (R10)(R9*1), X2
	MOVQ      1(R10)(R9*1), X3
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
	MOVQ      X0, (R11)
	ADDQ      R9, R10
	ADDQ      R9, R11
	DECQ      AX
	JNZ       chroma_cr_bilin_loop
	RET

// copyMacroblockAVX2 implements MPEG-1 block motion compensation using AVX2.
// AVX2 provides 256-bit YMM registers, allowing processing of 32 bytes at once
// for luma (vs 16 bytes with SSE2) and 16 bytes for chroma (vs 8 bytes).
//
// Function signature:
// func copyMacroblockNEON(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame)
//
// Registers used:
//   R8  = lumaWidth (luma stride)
//   R9  = chromaWidth (chroma stride)
//   R10 = *Frame s (source frame pointer)
//   R11 = *Frame d (destination frame pointer)
//   R12, R13, R14, R15 = temporaries for calculations
//
// AVX2 registers Y0-Y15 are used for SIMD operations.
//   Y15: all zeros (for unpacking/packing)
//   Y14: all words 1 (for rounding in linear interpolation)
//   Y13: all words 2 (for rounding in bilinear interpolation)
TEXT ·copyMacroblockAVX2(SB), NOSPLIT, $0-64

	// -- Load arguments from stack --
	MOVQ motionH+0(FP), AX      // motionH (horizontal) into AX
	MOVQ motionV+8(FP), BX      // motionV (vertical) into BX
	MOVQ mbRow+16(FP), CX       // mbRow into CX
	MOVQ mbCol+24(FP), DX       // mbCol into DX
	MOVQ lumaWidth+32(FP), R8   // lumaWidth (stride) into R8
	MOVQ chromaWidth+40(FP), R9 // chromaWidth (stride) into R9
	MOVQ s+48(FP), R10          // *Frame s (source) into R10
	MOVQ d+56(FP), R11          // *Frame d (dest) into R11

	// -- Prepare AVX2 rounding constants for SIMD --
	// Y15: all zero (used for unpack/pack). VPXOR clears Y15 to zero.
	VPXOR Y15, Y15, Y15

	// Y14: all words set to 1 (for linear interpolation rounding)
	// VPCMPEQW sets all words to -1 (0xFFFF), then VPSRLW shifts right by 15 to get 0x0001 per word.
	VPCMPEQW Y14, Y14, Y14
	VPSRLW   $15, Y14, Y14 // Y14 = 0x0001 per word (16 words in 256-bit register)

	// Y13: all words set to 2 (for bilinear interpolation rounding)
	// Copy Y14 (all 1s) to Y13, then VPADDW doubles it to all 2s.
	VPADDW Y14, Y14, Y13 // Y13 = 0x0002 per word

	// -- Compute luma source offsets --
	// luma: Y.Data + yoff*stride + xoff

	// Integer + fractional parts of motion vectors
	// R12 = hInt = motionH >> 1 (arithmetic shift preserves sign)
	MOVQ AX, R12
	SARQ $1, R12 // R12 = hInt = motionH >> 1
	MOVQ BX, R13
	SARQ $1, R13 // R13 = vInt = motionV >> 1
	MOVQ AX, R14
	ANDQ $1, R14 // R14 = hFrac (0 or 1)
	MOVQ BX, R15
	ANDQ $1, R15 // R15 = vFrac (0 or 1)

	// Compute luma source pixel index (row,col), then offset
	// rowSrc = (mbRow<<4) + vInt
	MOVQ CX, DI
	SHLQ $4, DI
	ADDQ R13, DI // DI = rowSrc

	// colSrc = (mbCol<<4) + hInt
	MOVQ DX, BP
	SHLQ $4, BP
	ADDQ R12, BP // BP = colSrc

	// SI = Y.Data pointer (from Frame)
	// In Frame struct, Y.Data is at offset 40
	MOVQ 40(R10), SI // struct Frame, Y.Data is at offset 40

	// Compute offset: SI += rowSrc*lumaWidth + colSrc
	// temp = rowSrc * lumaWidth
	MOVQ  DI, AX
	IMULQ R8, AX
	ADDQ  BP, AX
	ADDQ  AX, SI // SI now points to source luma data with motion offset

	// Compute destination luma offset (no motion compensation for dest)
	// rowDst = mbRow<<4, colDst = mbCol<<4
	MOVQ CX, DI
	SHLQ $4, DI
	MOVQ DX, BP
	SHLQ $4, BP
	MOVQ 40(R11), DI // d.Y.Data

	// temp = rowDst * lumaWidth
	MOVQ  CX, AX
	SHLQ  $4, AX
	IMULQ R8, AX
	ADDQ  BP, AX
	ADDQ  AX, DI // DI = dest Y pointer

	// -- Luma interpolation branching --
	// Based on fractional parts (hFrac, vFrac), jump to appropriate interpolation method.
	CMPQ R14, $0
	JE   luma_avx2_hf0      // hFrac == 0?
	CMPQ R15, $0
	JE   luma_avx2_h1vf0    // hFrac==1, vFrac==0: horizontal interpolation
	JMP  luma_avx2_bilinear // hFrac==1, vFrac==1: bilinear

luma_avx2_hf0:
	CMPQ R15, $0
	JE   luma_avx2_direct   // hFrac==0, vFrac==0: direct copy
	JMP  luma_avx2_vertical // hFrac==0, vFrac==1: vertical

// -- LUMA: Direct copy (no interpolation) using AVX2 --
// Process 16 bytes per row (luma is 16x16)
luma_avx2_direct:
	MOVQ $16, R12 // Loop over 16 rows

luma_avx2_direct_loop:
	// Load 16 bytes from source using VMOVDQU (unaligned load).
	// We only need 16 bytes for luma width, so use lower 128 bits of YMM register
	VMOVDQU (SI), X0              // Load 16 Y bytes from src (128 bits)
	VMOVDQU X0, (DI)              // Store to dest (unaligned store)
	ADDQ    R8, SI                // src += stride
	ADDQ    R8, DI                // dst += stride
	DECQ    R12                   // Decrement row counter
	JNZ     luma_avx2_direct_loop // Loop if not zero
	JMP     chroma_avx2_begin     // Proceed to chroma

// -- LUMA: Horizontal interpolation (average with right neighbor) using AVX2 --
luma_avx2_h1vf0:
	MOVQ $16, R12 // 16 rows

luma_avx2_horiz_loop:
	// Load current 16 bytes and next 16 bytes (shifted by 1 for right neighbors).
	VMOVDQU (SI), X0  // Load 16 bytes Y[n] (128 bits)
	VMOVDQU 1(SI), X1 // Load next 16 bytes Y[n+1] (128 bits)

	// Since we're processing 16 bytes, we can use 128-bit operations efficiently
	// Convert to 256-bit for word operations
	VPMOVZXBW X0, Y2 // Zero-extend 16 bytes to 16 words in Y2
	VPMOVZXBW X1, Y3 // Zero-extend 16 bytes to 16 words in Y3

	// Sum, add rounding, divide by 2
	VPADDW Y3, Y2, Y2  // Add corresponding words
	VPADDW Y14, Y2, Y2 // Add 1 to each word for rounding
	VPSRLW $1, Y2, Y2  // Divide by 2

	// Pack words back to bytes
	// Extract low and high 128-bit lanes
	VEXTRACTI128 $1, Y2, X3 // Extract high 128 bits to X3
	VPACKUSWB    X3, X2, X2 // Pack both halves back to bytes
	VMOVDQU      X2, (DI)   // Store 16 bytes

	ADDQ R8, SI               // Advance source
	ADDQ R8, DI               // Advance dest
	DECQ R12                  // Decrement counter
	JNZ  luma_avx2_horiz_loop // Loop
	JMP  chroma_avx2_begin    // To chroma

// -- LUMA: Vertical interpolation (average with row below) using AVX2 --
luma_avx2_vertical:
	MOVQ $16, R12 // 16 rows

luma_avx2_vert_loop:
	// Load current row and next row.
	VMOVDQU (SI), X0
	VMOVDQU (SI)(R8*1), X1 // Next row (stride R8)

	// Zero-extend to words for averaging
	VPMOVZXBW X0, Y2
	VPMOVZXBW X1, Y3

	VPADDW Y3, Y2, Y2  // Add
	VPADDW Y14, Y2, Y2 // Round
	VPSRLW $1, Y2, Y2  // Divide by 2

	// Pack back to bytes
	VEXTRACTI128 $1, Y2, X3
	VPACKUSWB    X3, X2, X2
	VMOVDQU      X2, (DI)

	ADDQ R8, SI
	ADDQ R8, DI
	DECQ R12
	JNZ  luma_avx2_vert_loop
	JMP  chroma_avx2_begin

// -- LUMA: Bilinear interpolation (average 4 neighbors) using AVX2 --
luma_avx2_bilinear:
	MOVQ $16, R12 // 16 rows

luma_avx2_bilin_loop:
	// Load four neighboring rows: current, right, below, below-right.
	VMOVDQU (SI), X0        // Y[i,j] (16 bytes)
	VMOVDQU 1(SI), X1       // Y[i,j+1]
	VMOVDQU (SI)(R8*1), X2  // Y[i+1,j]
	VMOVDQU 1(SI)(R8*1), X3 // Y[i+1,j+1]

	// Zero-extend all to words
	VPMOVZXBW X0, Y4
	VPMOVZXBW X1, Y5
	VPMOVZXBW X2, Y6
	VPMOVZXBW X3, Y7

	// Sum horizontally
	VPADDW Y5, Y4, Y4 // Y4 = Y[i,j] + Y[i,j+1]
	VPADDW Y7, Y6, Y6 // Y6 = Y[i+1,j] + Y[i+1,j+1]

	// Sum vertically
	VPADDW Y6, Y4, Y4 // Y4 = sum of all 4

	// Add 2 for rounding and divide by 4
	VPADDW Y13, Y4, Y4 // Add 2 for rounding
	VPSRLW $2, Y4, Y4  // Divide by 4

	// Pack back to bytes
	VEXTRACTI128 $1, Y4, X5
	VPACKUSWB    X5, X4, X4
	VMOVDQU      X4, (DI)

	ADDQ R8, SI
	ADDQ R8, DI
	DECQ R12
	JNZ  luma_avx2_bilin_loop

	// -- Compute chroma source offsets (Cb/Cr are half res of luma) --
chroma_avx2_begin:
	// Chroma motion vectors: (motionH/2), (motionV/2)
	// Truncate toward zero for signed division
	MOVQ motionH+0(FP), R12
	SARQ $1, R12            // chromaH = motionH >> 1 (arithmetic shift)
	MOVQ motionV+8(FP), R13
	SARQ $1, R13            // chromaV = motionV >> 1 (arithmetic shift)

	// Adjust for toward zero truncation if negative and odd
	MOVQ  motionH+0(FP), AX
	TESTQ AX, AX             // Check if motionH negative
	JGE   chroma_avx2_h_zero
	TESTQ $1, AX             // Check if odd
	JZ    chroma_avx2_h_zero
	ADDQ  $1, R12            // If negative and odd, add 1 to make less negative (toward zero)

chroma_avx2_h_zero:
	MOVQ R12, R14
	SARQ $1, R14  // chromaH_int = chromaH >> 1
	MOVQ R12, R15
	ANDQ $1, R15  // chromaH_frac = chromaH & 1

	// chromaH_int = R14, chromaH_frac = R15

	MOVQ  motionV+8(FP), AX
	TESTQ AX, AX
	JGE   chroma_avx2_v_zero
	TESTQ $1, AX
	JZ    chroma_avx2_v_zero
	ADDQ  $1, R13

chroma_avx2_v_zero:
	MOVQ R13, BX  // chromaV_int = BX
	SARQ $1, BX   // chromaV_int = chromaV >> 1
	MOVQ R13, R12 // chromaV_frac = R13 & 1
	ANDQ $1, R12

	// chromaV_int = BX, chromaV_frac = R12

	// Compute Cb/Cr source and dest pointers
	// src row = (mbRow<<3) + chromaV_int
	MOVQ mbRow+16(FP), SI
	SHLQ $3, SI
	ADDQ BX, SI

	// src col = (mbCol<<3) + chromaH_int
	MOVQ mbCol+24(FP), BP
	SHLQ $3, BP
	ADDQ R14, BP

	// src offset = src_row*chromaWidth + src_col
	MOVQ  SI, AX
	IMULQ R9, AX
	ADDQ  BP, AX

	// Source pointers
	// Cb.Data at offset 80, Cr.Data at 120 in Frame struct.
	MOVQ 80(R10), SI   // s.Cb.Data
	ADDQ AX, SI
	MOVQ 120(R10), R10 // s.Cr.Data
	ADDQ AX, R10

	// dest row = mbRow<<3
	MOVQ mbRow+16(FP), CX
	SHLQ $3, CX

	// dest col = mbCol<<3
	MOVQ mbCol+24(FP), BP
	SHLQ $3, BP

	// dest offset = dest_row*chromaWidth + dest_col
	MOVQ  CX, AX
	IMULQ R9, AX
	ADDQ  BP, AX

	// Dest pointers
	MOVQ 80(R11), DI   // d.Cb.Data
	ADDQ AX, DI
	MOVQ 120(R11), R11 // d.Cr.Data
	ADDQ AX, R11

	// -- Chroma interpolation branching for Cb --
	// Similar branching as luma, but for 8x8 blocks.
	CMPQ R15, $0
	JE   chroma_avx2_cb_hf0
	CMPQ R12, $0
	JE   chroma_avx2_cb_h1vf0
	JMP  chroma_avx2_cb_bilinear

chroma_avx2_cb_hf0:
	CMPQ R12, $0
	JE   chroma_avx2_cb_direct
	JMP  chroma_avx2_cb_vertical

// -- CHROMA Cb: Direct copy --
chroma_avx2_cb_direct:
	MOVQ $8, AX // 8 rows for chroma

chroma_avx2_cb_direct_loop:
	// Direct copy 8 bytes using MOVQ
	MOVQ (SI), X0
	MOVQ X0, (DI)
	ADDQ R9, SI
	ADDQ R9, DI
	DECQ AX
	JNZ  chroma_avx2_cb_direct_loop
	JMP  chroma_avx2_cr_begin

// -- CHROMA Cb: Horizontal interpolation --
chroma_avx2_cb_h1vf0:
	MOVQ $8, AX

chroma_avx2_cb_horiz_loop:
	// Load 8 bytes and next 8 (shifted by 1).
	MOVQ (SI), X0
	MOVQ 1(SI), X1

	// Zero-extend to words
	VPMOVZXBW X0, X2
	VPMOVZXBW X1, X3

	VPADDW X3, X2, X2
	VPADDW X14, X2, X2 // Round
	VPSRLW $1, X2, X2  // Divide

	// Pack back to bytes (only need low 8 bytes)
	VPACKUSWB X2, X2, X2
	MOVQ      X2, (DI)

	ADDQ R9, SI
	ADDQ R9, DI
	DECQ AX
	JNZ  chroma_avx2_cb_horiz_loop
	JMP  chroma_avx2_cr_begin

// -- CHROMA Cb: Vertical interpolation --
chroma_avx2_cb_vertical:
	MOVQ $8, AX

chroma_avx2_cb_vert_loop:
	MOVQ (SI), X0
	MOVQ (SI)(R9*1), X1

	VPMOVZXBW X0, X2
	VPMOVZXBW X1, X3

	VPADDW X3, X2, X2
	VPADDW X14, X2, X2
	VPSRLW $1, X2, X2

	VPACKUSWB X2, X2, X2
	MOVQ      X2, (DI)

	ADDQ R9, SI
	ADDQ R9, DI
	DECQ AX
	JNZ  chroma_avx2_cb_vert_loop
	JMP  chroma_avx2_cr_begin

// -- CHROMA Cb: Bilinear interpolation --
chroma_avx2_cb_bilinear:
	MOVQ $8, AX

chroma_avx2_cb_bilin_loop:
	MOVQ (SI), X0
	MOVQ 1(SI), X1
	MOVQ (SI)(R9*1), X2
	MOVQ 1(SI)(R9*1), X3

	VPMOVZXBW X0, X4
	VPMOVZXBW X1, X5
	VPMOVZXBW X2, X6
	VPMOVZXBW X3, X7

	VPADDW X5, X4, X4
	VPADDW X7, X6, X6
	VPADDW X6, X4, X4
	VPADDW X13, X4, X4 // Round by 2
	VPSRLW $2, X4, X4  // Divide by 4

	VPACKUSWB X4, X4, X4
	MOVQ      X4, (DI)

	ADDQ R9, SI
	ADDQ R9, DI
	DECQ AX
	JNZ  chroma_avx2_cb_bilin_loop

	// -- Chroma Cr plane --
chroma_avx2_cr_begin:
	// R10: s.Cr.Data, R11: d.Cr.Data
	// Use same chroma motion as above (R15: hFrac, R12: vFrac)
	// Branching similar to Cb.
	CMPQ R15, $0
	JE   chroma_avx2_cr_hf0
	CMPQ R12, $0
	JE   chroma_avx2_cr_h1vf0
	JMP  chroma_avx2_cr_bilinear

chroma_avx2_cr_hf0:
	CMPQ R12, $0
	JE   chroma_avx2_cr_direct
	JMP  chroma_avx2_cr_vertical

// -- CHROMA Cr: Direct copy --
chroma_avx2_cr_direct:
	MOVQ $8, AX

chroma_avx2_cr_direct_loop:
	MOVQ (R10), X0
	MOVQ X0, (R11)
	ADDQ R9, R10
	ADDQ R9, R11
	DECQ AX
	JNZ  chroma_avx2_cr_direct_loop
	RET                             // End of function

// -- CHROMA Cr: Horizontal interpolation --
chroma_avx2_cr_h1vf0:
	MOVQ $8, AX

chroma_avx2_cr_horiz_loop:
	MOVQ (R10), X0
	MOVQ 1(R10), X1

	VPMOVZXBW X0, X2
	VPMOVZXBW X1, X3

	VPADDW X3, X2, X2
	VPADDW X14, X2, X2
	VPSRLW $1, X2, X2

	VPACKUSWB X2, X2, X2
	MOVQ      X2, (R11)

	ADDQ R9, R10
	ADDQ R9, R11
	DECQ AX
	JNZ  chroma_avx2_cr_horiz_loop
	RET

// -- CHROMA Cr: Vertical interpolation --
chroma_avx2_cr_vertical:
	MOVQ $8, AX

chroma_avx2_cr_vert_loop:
	MOVQ (R10), X0
	MOVQ (R10)(R9*1), X1

	VPMOVZXBW X0, X2
	VPMOVZXBW X1, X3

	VPADDW X3, X2, X2
	VPADDW X14, X2, X2
	VPSRLW $1, X2, X2

	VPACKUSWB X2, X2, X2
	MOVQ      X2, (R11)

	ADDQ R9, R10
	ADDQ R9, R11
	DECQ AX
	JNZ  chroma_avx2_cr_vert_loop
	RET

// -- CHROMA Cr: Bilinear interpolation --
chroma_avx2_cr_bilinear:
	MOVQ $8, AX

chroma_avx2_cr_bilin_loop:
	MOVQ (R10), X0
	MOVQ 1(R10), X1
	MOVQ (R10)(R9*1), X2
	MOVQ 1(R10)(R9*1), X3

	VPMOVZXBW X0, X4
	VPMOVZXBW X1, X5
	VPMOVZXBW X2, X6
	VPMOVZXBW X3, X7

	VPADDW X5, X4, X4
	VPADDW X7, X6, X6
	VPADDW X6, X4, X4
	VPADDW X13, X4, X4
	VPSRLW $2, X4, X4

	VPACKUSWB X4, X4, X4
	MOVQ      X4, (R11)

	ADDQ R9, R10
	ADDQ R9, R11
	DECQ AX
	JNZ  chroma_avx2_cr_bilin_loop
	RET

// hasAVX2 returns true if the CPU supports AVX2 instructions and the OS enables their use.
// This function uses CPUID to check hardware support and XGETBV to check OS support.
TEXT ·hasAVX2(SB), NOSPLIT, $0-1
	// First, check if CPUID supports function 7 (maxID >= 7).
	// CPUID with EAX=0 returns the maximum function number in EAX.
	MOVL $0, AX     // Set EAX to 0 for maximum function query.
	CPUID           // Execute CPUID; results in EAX, EBX, ECX, EDX.
	CMPL AX, $7     // Compare max function with 7.
	JL   no_support // If less than 7, jump to no_support (AVX2 requires function 7).

	// Now, query CPUID function 1 to check for OSXSAVE (bit 27 in ECX).
	// OSXSAVE indicates the OS supports saving extended states with XSAVE/XRSTOR.
	MOVL $1, AX     // Set EAX to 1 for basic feature info.
	MOVL $0, CX     // Set ECX to 0 (subleaf).
	CPUID           // Execute CPUID.
	BTL  $27, CX    // Test bit 27 in ECX (OSXSAVE).
	JNC  no_support // If not set (no carry), jump to no_support.

	// If OSXSAVE is supported, use XGETBV to check OS support for AVX.
	// XGETBV with ECX=0 returns the XCR0 register in EAX:EDX.
	// For AVX, we need bits 1 (XMM) and 2 (YMM) set in XCR0.
	MOVL $0, CX     // Set ECX to 0 for XCR0.
	XGETBV          // Execute XGETBV; result in EAX (low) and EDX (high).
	ANDL $6, AX     // Mask bits 1 and 2 (0b110).
	CMPL AX, $6     // Check if both bits are set.
	JNE  no_support // If not equal, jump to no_support.

	// Finally, query CPUID function 7, subleaf 0, for AVX2 support.
	// AVX2 is indicated by bit 5 in EBX.
	MOVL $7, AX     // Set EAX to 7 for extended features.
	MOVL $0, CX     // Set ECX to 0 (subleaf).
	CPUID           // Execute CPUID.
	BTL  $5, BX     // Test bit 5 in EBX (AVX2).
	JNC  no_support // If not set, jump to no_support.

	// If all checks pass, return true.
	MOVB $1, ret+0(FP) // Set return value to 1 (true). Note: bool is 1 byte.
	RET                // Return.

no_support:
	// Return false if any check fails.
	MOVB $0, ret+0(FP) // Set return value to 0 (false).
	RET

