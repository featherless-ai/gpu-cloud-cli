package volume

import (
	"context"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
)

const (
	waitPoll     = time.Second
	waitMaxPolls = 30
)

// Seam tests replace: how --wait pauses between polls.
var sleep = time.Sleep

func itoa(value int) string {
	return strconv.Itoa(value)
}

func parseCapacity(value string) (int, error) {
	capacity := jsNumber(value)

	if !isInteger(capacity) || capacity < 10 || capacity > 10240 {
		return 0, errs.New("Capacity must be between 10 and 10240 GB.")
	}

	return int(capacity), nil
}

// findAndGetVolume resolves a volume name or id and reads the volume in full.
func findAndGetVolume(ctx context.Context, client api.Cloud, ref string) (api.NetworkVolume, error) {
	volumes, err := client.ListVolumes(ctx)

	if err != nil {
		return api.NetworkVolume{}, err
	}

	found, err := shared.FindVolume(volumes, ref)

	if err != nil {
		return api.NetworkVolume{}, err
	}

	return client.GetVolume(ctx, found.ID)
}

func isUsable(state api.VolumeState) bool {
	return state == api.VolumeAvailable || state == api.VolumeInUse
}

func newCreateCommand() *cobra.Command {
	var opts struct {
		gb      string
		wait    bool
		profile string
	}

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a network volume",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := shared.NewContext(cmd)
			ctx := cmd.Context()
			client, err := shared.Client(opts.profile)

			if err != nil {
				return err
			}

			capacity, err := parseCapacity(opts.gb)

			if err != nil {
				return err
			}

			body := api.CreateVolumeBody{Name: args[0], CapacityGiB: capacity}
			volume, err := client.CreateVolume(ctx, body, shared.NewIdempotencyKey())

			if err != nil {
				return err
			}

			final := volume

			if opts.wait {
				for attempt := 0; attempt < waitMaxPolls; attempt++ {
					if isUsable(final.State) || final.State == api.VolumeError {
						break
					}

					sleep(waitPoll)
					final, err = client.GetVolume(ctx, volume.ID)

					if err != nil {
						return err
					}
				}
			}

			if final.State == api.VolumeError {
				reason := "unknown error"

				if final.LastError != nil {
					reason = *final.LastError
				}

				return out.Fatal("Volume " + final.Name + " failed to provision: " + reason)
			}

			switch {
			case isUsable(final.State):
				out.Success("Network volume " + final.Name + " is ready.")
			case opts.wait:
				out.Warn("Volume " + final.Name + " is still " + string(final.State) + ". Check with featherless-cloud volume get " + final.ID + ".")
			default:
				out.Success("Network volume " + final.Name + " is provisioning. It becomes usable automatically — check with featherless-cloud volume get.")
			}

			out.Output(final, func() output.Result { return output.Result{Text: "STATE " + string(final.State)} })

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "name"})
	shared.Option(cmd, &opts.gb, "--gb <n>", "100", "capacity in GB (10-10240)")
	shared.Switch(cmd, &opts.wait, "--wait", "wait until the volume is usable")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
