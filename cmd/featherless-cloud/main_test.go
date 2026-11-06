package main

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The test binary doubles as the CLI when this is set.
func TestMain(m *testing.M) {
	if os.Getenv("FEATHERLESS_CLOUD_TEST_AS_CLI") == "1" {
		main()
		return
	}

	os.Exit(m.Run())
}

const instanceJSON = `{"id":"6f1c2d3e-4a5b-4c6d-8e7f-901a2b3c4d5e","shortId":"bcdfghjkmn","name":"ticker","computeLabel":"2 vCPU","computeType":"cpu","gpuCount":0,"state":"running","startupStage":null,"costMicrosPerHour":"1","ephemeralDiskGiB":20,"createdAt":"2026-11-06T00:00:00.000Z"}`

// A script's background job starts with Ctrl-C ignored. Node still stops on
// Ctrl-C there (exit 130), so `featherless-cloud … --follow &` followed by
// kill -INT ends the TypeScript CLI; the Go CLI must too.
func TestCtrlCEndsTheCLIEvenWhenItStartsIgnored(t *testing.T) {
	for _, c := range []struct {
		signal syscall.Signal
		code   int
	}{{syscall.SIGINT, 130}} {
		t.Run(c.signal.String(), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")

				if strings.HasSuffix(r.URL.Path, "/logs") {
					fmt.Fprint(w, `{"logs":"tick\n"}`)
					return
				}

				fmt.Fprint(w, instanceJSON)
			}))
			defer server.Close()

			home := t.TempDir()
			config := fmt.Sprintf("version: 1\ncurrentProfile: work\nprofiles:\n  work:\n    apiBase: %s\n    apiKey: flkey_test\n    createdAt: 2026-11-01T00:00:00.000Z\n", server.URL)

			if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(filepath.Join(home, ".config", "featherless-cloud.yaml"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}

			// Children inherit ignored signals, as with `cmd &` in a script.
			signal.Ignore(c.signal)
			defer signal.Reset(c.signal)

			cmd := exec.Command(os.Args[0], "--non-interactive", "instance", "logs", "bcdfghjkmn", "--follow")
			cmd.Env = append(os.Environ(), "FEATHERLESS_CLOUD_TEST_AS_CLI=1", "HOME="+home, "CI=1")
			stdout, _ := cmd.StdoutPipe()

			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}

			firstLine := make(chan string, 1)
			go func() {
				line, _ := bufio.NewReader(stdout).ReadString('\n')
				firstLine <- line
			}()

			select {
			case <-firstLine:
			case <-time.After(10 * time.Second):
				_ = cmd.Process.Kill()
				t.Fatal("no log line within 10 s")
			}

			_ = cmd.Process.Signal(c.signal)
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()

			select {
			case err := <-exited:
				exitErr, isExit := err.(*exec.ExitError)

				if !isExit || exitErr.ExitCode() != c.code {
					t.Errorf("exit = %v, want code %d", err, c.code)
				}
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				t.Errorf("still running 5 s after %v", c.signal)
			}
		})
	}
}
