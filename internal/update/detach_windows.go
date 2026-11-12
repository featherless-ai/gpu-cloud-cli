//go:build windows

package update

import "syscall"

const detachedProcessFlag = 0x00000008

// No console and its own process group, so the check outlives the CLI and Ctrl-C.
func detachedProcess() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: detachedProcessFlag | syscall.CREATE_NEW_PROCESS_GROUP}
}
