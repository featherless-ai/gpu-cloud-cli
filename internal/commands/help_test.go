package commands

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// commandPaths are the words that reach every command of the tree.
func commandPaths(cmd *cobra.Command, path []string) [][]string {
	paths := [][]string{path}

	for _, child := range cmd.Commands() {
		childPath := append(append([]string{}, path...), child.Name())
		paths = append(paths, commandPaths(child, childPath)...)
	}

	return paths
}

func TestEveryCommandPrintsItsHelp(t *testing.T) {
	for _, path := range commandPaths(NewRoot(), nil) {
		args := append(append([]string{}, path...), "--help")

		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var help bytes.Buffer
			root := NewRoot()
			root.SetOut(&help)
			root.SetErr(&help)
			root.SetArgs(args)

			err := func() (err error) {
				defer func() {
					if recovered := recover(); recovered != nil {
						err = fmt.Errorf("panic: %v", recovered)
					}
				}()

				return root.Execute()
			}()

			if err != nil {
				t.Fatalf("error = %v", err)
			}

			if !strings.Contains(help.String(), "Usage:") {
				t.Errorf("no help printed:\n%s", help.String())
			}
		})
	}
}
