// Package browser opens the default browser. The URL is always printed too,
// so a failed launch is harmless.
package browser

import (
	"os/exec"
	"runtime"
)

func Open(url string) {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}

	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
}
