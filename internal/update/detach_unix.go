//go:build unix

package update

import "syscall"

// A new session keeps the check running after the CLI exits or the terminal sends a signal.
func detachedProcess() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
