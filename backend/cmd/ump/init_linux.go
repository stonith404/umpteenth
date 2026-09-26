package main

import "syscall"

// becomeSubreaper makes orphaned descendants reparent to this process instead of the real PID 1
func becomeSubreaper() {
	const prSetChildSubreaper = 36
	_, _, _ = syscall.RawSyscall(syscall.SYS_PRCTL, prSetChildSubreaper, 1, 0)
}
