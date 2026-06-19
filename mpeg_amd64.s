//go:build amd64 && !noasm

#include "textflag.h"

// hasAVX2 reports whether the CPU supports AVX2 and the OS has enabled the
// extended (YMM) register state. It checks CPUID feature bits and confirms via
// XGETBV that XCR0 advertises XMM and YMM saving.
TEXT ·hasAVX2(SB), NOSPLIT, $0-1
	// Require CPUID leaf 7 to exist.
	MOVL $0, AX
	CPUID
	CMPL AX, $7
	JL   no_support

	// Leaf 1: OSXSAVE (ECX bit 27) must be set before XGETBV is valid.
	MOVL $1, AX
	MOVL $0, CX
	CPUID
	BTL  $27, CX
	JNC  no_support

	// XCR0 must advertise XMM (bit 1) and YMM (bit 2) state saving.
	MOVL $0, CX
	XGETBV
	ANDL $6, AX
	CMPL AX, $6
	JNE  no_support

	// Leaf 7, subleaf 0: AVX2 is EBX bit 5.
	MOVL $7, AX
	MOVL $0, CX
	CPUID
	BTL  $5, BX
	JNC  no_support

	MOVB $1, ret+0(FP)
	RET

no_support:
	MOVB $0, ret+0(FP)
	RET
