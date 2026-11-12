package commands

import (
	"testing"

	"featherless.ai/featherless-cloud/internal/errs"
)

// The volume commands reject a wrong number of arguments with commander's
// words, as the TypeScript CLI did.
func TestVolumeCommandsReportArgumentMistakesLikeTheTypeScriptCLI(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"volume", "create"}, "missing required argument 'name'"},
		{[]string{"volume", "get"}, "missing required argument 'id'"},
		{[]string{"volume", "update"}, "missing required argument 'id'"},
		{[]string{"volume", "delete"}, "missing required argument 'id'"},
		{[]string{"volume", "find"}, "missing required argument 'path'"},
		{[]string{"volume", "mkdir"}, "missing required argument 'paths'"},
		{[]string{"volume", "rm"}, "missing required argument 'paths'"},
		{[]string{"volume", "cp"}, "missing required argument 'paths'"},
		{[]string{"volume", "mv"}, "missing required argument 'paths'"},
		{[]string{"volume", "sync"}, "missing required argument 'local-folder'"},
		{[]string{"volume", "sync", "a"}, "missing required argument 'remote-folder'"},
		{[]string{"volume", "get", "a", "b"}, "too many arguments for 'get'. Expected 1 argument but got 2."},
		{[]string{"volume", "ls", "a", "b"}, "too many arguments for 'ls'. Expected 1 argument but got 2."},
		{[]string{"volume", "sync", "a", "b", "c"}, "too many arguments for 'sync'. Expected 2 arguments but got 3."},
		{[]string{"volume", "create", "a", "b"}, "too many arguments for 'create'. Expected 1 argument but got 2."},
	}

	for _, c := range cases {
		root := NewRoot()
		root.SetArgs(c.args)
		err := root.Execute()

		if err == nil || err.Error() != c.want || !errs.IsKind(err, errs.KindUsage) {
			t.Errorf("%v: got %v, want usage error %q", c.args, err, c.want)
		}
	}
}
