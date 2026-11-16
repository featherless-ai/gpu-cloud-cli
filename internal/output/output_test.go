package output

import (
	"bytes"
	"testing"
)

// Interactive mode prints plain text with no gutter or symbols, so people and
// agents read the same words; only a table gets blank lines around it.
func TestInteractiveModePrintsPlainTextWithTablesSetOff(t *testing.T) {
	wantStdouts := map[Mode]string{
		Interactive: "Create an API key.\nVolume data updated.\n\nNAME\n----\ndata\n\n1 volume(s)\n",
		Plain:       "Create an API key.\nVolume data updated.\nNAME\n----\ndata\n1 volume(s)\n",
	}

	for mode, wantStdout := range wantStdouts {
		var stdout, stderr bytes.Buffer
		ctx := &Context{Mode: mode, Stdout: &stdout, Stderr: &stderr}

		ctx.Log("Create an API key.")
		ctx.Success("Volume data updated.")
		ctx.Warn("Organization GPU limit already reached.")
		ctx.Output(nil, func() Result {
			return Result{Table: &Table{Header: []string{"NAME"}, Rows: [][]string{{"data"}}}, Footer: "1 volume(s)"}
		})

		wantStderr := "Warning: Organization GPU limit already reached.\n"

		if stdout.String() != wantStdout {
			t.Errorf("mode %d stdout = %q, want %q", mode, stdout.String(), wantStdout)
		}

		if stderr.String() != wantStderr {
			t.Errorf("mode %d stderr = %q, want %q", mode, stderr.String(), wantStderr)
		}
	}
}
