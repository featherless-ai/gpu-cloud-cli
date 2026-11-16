package commands

import (
	"io"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/output"
	"featherless.ai/featherless-cloud/internal/update"
	"featherless.ai/featherless-cloud/internal/version"
)

type upgradeResult struct {
	Package  string `json:"package"`
	Current  string `json:"current"`
	Latest   string `json:"latest"`
	Upgraded bool   `json:"upgraded"`
}

func newUpgradeCommand() *cobra.Command {
	var opts struct {
		force bool
	}

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade featherless-cloud to the latest published version",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)
			current := version.Version

			latest, err := update.FetchLatestVersion(cmd.Context())

			if err != nil {
				return err
			}

			if !opts.force && !version.IsNewer(latest, current) {
				result := upgradeResult{Package: version.PackageName, Current: current, Latest: latest, Upgraded: false}
				ctx.Output(result, func() output.Result { return output.Result{Text: "Already up to date (" + current + ")."} })

				return nil
			}

			// The installer's output keeps stdout one JSON document in --json mode.
			var installerOutput io.Writer = ctx.Stdout

			if ctx.Mode == output.JSON {
				installerOutput = ctx.Stderr
			}

			// npm and brew print their own progress; a self-update downloads quietly.
			err = update.Upgrade(cmd.Context(), update.CurrentInstall(), latest, installerOutput)

			if err != nil {
				return err
			}

			result := upgradeResult{Package: version.PackageName, Current: current, Latest: latest, Upgraded: true}
			ctx.Output(result, func() output.Result { return output.Result{Text: "Upgraded " + current + " → " + latest + "."} })

			return nil
		},
	}

	shared.Switch(cmd, &opts.force, "--force", "reinstall even when already up to date")

	return cmd
}
