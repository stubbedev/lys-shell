package filechooser

import "golang.org/x/sys/unix"

// auditArch is the seccomp arch the filter allows calls of.
const auditArch = unix.AUDIT_ARCH_AARCH64

// archDeniedSyscalls are the denied calls only this arch has.
var archDeniedSyscalls []uint32
