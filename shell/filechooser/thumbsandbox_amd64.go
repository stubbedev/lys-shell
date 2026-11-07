package filechooser

import "golang.org/x/sys/unix"

// auditArch is the seccomp arch the filter allows calls of.
const auditArch = unix.AUDIT_ARCH_X86_64

// archDeniedSyscalls are the denied calls only this arch has.
var archDeniedSyscalls = []uint32{unix.SYS_USELIB, unix.SYS_MODIFY_LDT, unix.SYS_IOPL, unix.SYS_IOPERM}
