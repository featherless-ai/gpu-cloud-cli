package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// testdata/help.json is the TypeScript CLI's help, recorded for every command
// and every way of asking for it: --help, -h, help [command], an alias, and a
// command group run without a subcommand.
func TestHelpMatchesTheTypeScriptCLI(t *testing.T) {
	recorded, err := os.ReadFile("testdata/help.json")
	if err != nil {
		t.Fatal(err)
	}

	var cases []struct {
		Args   []string `json:"args"`
		Stdout string   `json:"stdout"`
		Stderr string   `json:"stderr"`
		Exit   int      `json:"exit"`
	}

	if err := json.Unmarshal(recorded, &cases); err != nil {
		t.Fatal(err)
	}

	for _, c := range cases {
		t.Run(strings.Join(append([]string{"featherless-cloud"}, c.Args...), " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			cmd := exec.Command(os.Args[0], c.Args...)
			cmd.Env = append(os.Environ(), "FEATHERLESS_CLOUD_TEST_AS_CLI=1", "HOME="+t.TempDir(), "CI=1")
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()

			exit := 0
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exit = exitErr.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}

			if stdout.String() != c.Stdout {
				t.Errorf("stdout:\n--- TypeScript\n%s\n--- Go\n%s", c.Stdout, stdout.String())
			}

			if stderr.String() != c.Stderr {
				t.Errorf("stderr:\n--- TypeScript\n%s\n--- Go\n%s", c.Stderr, stderr.String())
			}

			if exit != c.Exit {
				t.Errorf("exit code %d, want %d", exit, c.Exit)
			}
		})
	}
}
