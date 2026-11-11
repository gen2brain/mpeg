//go:build riscv64 && riscv64.rva23u64 && !noasm

#include "textflag.h"

// func synthWindowRVV(u *[32]float32, d *[1024]float32, v *[1024]float32, vPos int)
TEXT ·synthWindowRVV(SB), NOSPLIT, $0-32
	MOV u+0(FP), X10
	MOV d+8(FP), X11
	MOV v+16(FP), X12
	MOV vPos+24(FP), X13

	MOV $32, X5

lanes:
	VSETVLI X5, E32, M4, TA, MA, X16
	VMVVI   $0, V0

	SRL  $1, X13, X6
	MOV  $512, X7
	SUB  X6, X7
	AND  $127, X13, X8
	SRL  $1, X8
	MOV  $1024, X9

loop1:
	BGE   X8, X9, loop1done
	SLL   $2, X7, X14
	ADD   X11, X14
	SLL   $2, X8, X15
	ADD   X12, X15
	VLE32V (X14), V8
	VLE32V (X15), V16
	VFMACCVV V16, V8, V0
	ADD   $128, X8
	ADD   $64, X7
	JMP   loop1

loop1done:
	SUB  $480, X7
	MOV  $1120, X6
	SUB  X8, X6, X8

loop2:
	BGE   X8, X9, loop2done
	SLL   $2, X7, X14
	ADD   X11, X14
	SLL   $2, X8, X15
	ADD   X12, X15
	VLE32V (X14), V8
	VLE32V (X15), V16
	VFMACCVV V16, V8, V0
	ADD   $128, X8
	ADD   $64, X7
	JMP   loop2

loop2done:
	VSE32V V0, (X10)
	SUB    X16, X5
	SLL    $2, X16
	ADD    X16, X10
	ADD    X16, X11
	ADD    X16, X12
	BNEZ   X5, lanes
	RET
