//go:build riscv64 && riscv64.rva23u64 && !noasm

#include "textflag.h"

// func copyBlockRVV(dst, src *uint8, stride, size, mode int, avg bool)
//
// One plane of copyMacroblock: size rows of size bytes. mode is hFrac | vFrac<<1.
// Taps are widened to 16 bits, summed, rounded and narrowed back, so the
// single-axis average is (a+b+1)>>1, bilinear (a+b+c+d+2)>>2, and with avg the
// result is averaged into dst the same way. E16/M2 holds 16 lanes at VLEN 128.
TEXT ·copyBlockRVV(SB), NOSPLIT, $0-41
	MOV  dst+0(FP), X10
	MOV  src+8(FP), X11
	MOV  stride+16(FP), X12
	MOV  size+24(FP), X13
	MOV  mode+32(FP), X14
	MOVBU avg+40(FP), X15
	MOV  X13, X16

	MOV  $1, X5
	BEQ  X14, X5, horiz
	MOV  $2, X5
	BEQ  X14, X5, vert
	MOV  $3, X5
	BEQ  X14, X5, bilin

direct:
	VSETVLI  X13, E16, M2, TA, MA, X0
	VLE8V    (X11), V16
	VZEXTVF2 V16, V2
	BEQZ     X15, direct_store
	VLE8V    (X10), V20
	VZEXTVF2 V20, V10
	VADDVV   V10, V2, V2
	VADDVI   $1, V2, V2
	VSRLVI   $1, V2, V2

direct_store:
	VSETVLI X13, E8, M1, TA, MA, X0
	VNSRLWI $0, V2, V22
	VSE8V   V22, (X10)
	ADD     X12, X10
	ADD     X12, X11
	ADD     $-1, X16
	BNEZ    X16, direct
	RET

horiz:
	ADD $1, X11, X17

horiz_row:
	VSETVLI  X13, E16, M2, TA, MA, X0
	VLE8V    (X11), V16
	VZEXTVF2 V16, V2
	VLE8V    (X17), V17
	VZEXTVF2 V17, V4
	VADDVV   V4, V2, V2
	VADDVI   $1, V2, V2
	VSRLVI   $1, V2, V2
	BEQZ     X15, horiz_store
	VLE8V    (X10), V20
	VZEXTVF2 V20, V10
	VADDVV   V10, V2, V2
	VADDVI   $1, V2, V2
	VSRLVI   $1, V2, V2

horiz_store:
	VSETVLI X13, E8, M1, TA, MA, X0
	VNSRLWI $0, V2, V22
	VSE8V   V22, (X10)
	ADD     X12, X10
	ADD     X12, X11
	ADD     X12, X17
	ADD     $-1, X16
	BNEZ    X16, horiz_row
	RET

vert:
	ADD X12, X11, X17

vert_row:
	VSETVLI  X13, E16, M2, TA, MA, X0
	VLE8V    (X11), V16
	VZEXTVF2 V16, V2
	VLE8V    (X17), V17
	VZEXTVF2 V17, V4
	VADDVV   V4, V2, V2
	VADDVI   $1, V2, V2
	VSRLVI   $1, V2, V2
	BEQZ     X15, vert_store
	VLE8V    (X10), V20
	VZEXTVF2 V20, V10
	VADDVV   V10, V2, V2
	VADDVI   $1, V2, V2
	VSRLVI   $1, V2, V2

vert_store:
	VSETVLI X13, E8, M1, TA, MA, X0
	VNSRLWI $0, V2, V22
	VSE8V   V22, (X10)
	ADD     X12, X10
	ADD     X12, X11
	ADD     X12, X17
	ADD     $-1, X16
	BNEZ    X16, vert_row
	RET

bilin:
	ADD $1, X11, X17
	ADD X12, X11, X18
	ADD $1, X18, X19

bilin_row:
	VSETVLI  X13, E16, M2, TA, MA, X0
	VLE8V    (X11), V16
	VZEXTVF2 V16, V2
	VLE8V    (X17), V17
	VZEXTVF2 V17, V4
	VLE8V    (X18), V18
	VZEXTVF2 V18, V6
	VLE8V    (X19), V19
	VZEXTVF2 V19, V8
	VADDVV   V4, V2, V2
	VADDVV   V8, V6, V6
	VADDVV   V6, V2, V2
	VADDVI   $2, V2, V2
	VSRLVI   $2, V2, V2
	BEQZ     X15, bilin_store
	VLE8V    (X10), V20
	VZEXTVF2 V20, V10
	VADDVV   V10, V2, V2
	VADDVI   $1, V2, V2
	VSRLVI   $1, V2, V2

bilin_store:
	VSETVLI X13, E8, M1, TA, MA, X0
	VNSRLWI $0, V2, V22
	VSE8V   V22, (X10)
	ADD     X12, X10
	ADD     X12, X11
	ADD     X12, X17
	ADD     X12, X18
	ADD     X12, X19
	ADD     $-1, X16
	BNEZ    X16, bilin_row
	RET
