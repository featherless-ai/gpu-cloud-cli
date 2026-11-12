package commands

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/config"
	"featherless.ai/featherless-cloud/internal/errs"
)

func newLogoutCommand() *cobra.Command {
	var opts struct {
		profile string
	}

	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials for a profile",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			cfg, err := shared.LoadConfig()
			if err != nil {
				return err
			}

			name := cfg.CurrentProfile

			if cmd.Flags().Changed("profile") {
				name = opts.profile
			}

			// Inside an instance the CLI signs in with the instance's own key,
			// which is not this command's to remove.
			if _, isInstance := config.InstanceKey(); name == "" && isInstance {
				ctx.Log("This instance signs in automatically.")
				return nil
			}

			if _, exists := cfg.Profiles.Get(name); name == "" || !exists {
				return errs.New("Not logged in.")
			}

			cfg.Profiles.Delete(name)

			if cfg.CurrentProfile == name {
				cfg.CurrentProfile = ""
			}

			if err := config.Save(cfg); err != nil {
				return err
			}

			ctx.Success("Signed out of profile \"" + name + "\". Note: revoke the key at " + config.ConsoleBaseURL() + "/credentials/api-keys to fully disconnect this device.")

			return nil
		},
	}

	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to remove (default: current)")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
