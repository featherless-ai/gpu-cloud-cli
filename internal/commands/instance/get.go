package instance

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/output"
	"featherless.ai/featherless-cloud/internal/version"
)

// uptime is the wall time since the container started, as the console shows it.
func uptime(runtimeStartedAt string, now time.Time) string {
	started, err := time.Parse(time.RFC3339Nano, runtimeStartedAt)

	if err != nil {
		return "<1m"
	}

	elapsedMinutes := int(math.Max(0, math.Floor(now.Sub(started).Minutes())))
	days := elapsedMinutes / 1_440
	hours := (elapsedMinutes % 1_440) / 60
	minutes := elapsedMinutes % 60

	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	case elapsedMinutes > 0:
		return fmt.Sprintf("%dm", elapsedMinutes)
	}

	return "<1m"
}

func computeLabel(instance api.Instance) string {
	resources := instance.Resources

	if resources == nil {
		return instance.ComputeLabel
	}

	if instance.ComputeType == api.ComputeGPU {
		model := instance.ComputeLabel

		if resources.GPUModel != nil {
			model = *resources.GPUModel
		}

		return fmt.Sprintf("%d x %s", instance.GPUCount, model)
	}

	return fmt.Sprintf("%d cores / %d GiB", resources.CPUCores, resources.MemoryGiB)
}

func describeInstance(item api.Instance, now time.Time) string {
	lines := []string{
		"NAME        " + item.Name,
		"SHORT ID    " + item.ShortID,
		"STATE       " + stateLabel(item.State, item.StartupStage),
		"HOURLY      " + version.FormatDollars(item.CostMicrosPerHour) + "/hr",
		"COMPUTE     " + computeLabel(item),
		fmt.Sprintf("DISK        %d GiB container", item.EphemeralDiskGiB),
	}

	for _, mount := range item.NetworkVolumeMounts {
		lines = append(lines, fmt.Sprintf("VOLUMES     %s → %s", mount.Volume.Name, mount.MountPath))
	}

	for _, port := range item.PublicPorts {
		url := "unavailable"

		if port.URL != nil {
			url = *port.URL
		}

		lines = append(lines, fmt.Sprintf("PORT        %d → %s", port.Port, url))
	}

	if item.Image != nil {
		lines = append(lines, "IMAGE       "+item.Image.Reference)
	}

	templateVersion := item.TemplateVersion.Value

	if template := item.Template.Value; template != nil {
		parts := []string{}

		if template.Name != "" {
			parts = append(parts, template.Name)
		}

		if templateVersion != nil && templateVersion.Version != "" {
			parts = append(parts, templateVersion.Version)
		}

		lines = append(lines, "TEMPLATE    "+strings.Join(parts, " "))
	} else if templateVersion != nil {
		lines = append(lines, "TEMPLATE    "+templateVersion.Name+" "+templateVersion.Version)
	}

	if item.Command != nil {
		command := "Template default"

		if len(item.Command) > 0 {
			command = strings.Join(item.Command, " ")
		}

		lines = append(lines, "COMMAND     "+command)
	}

	for _, variable := range item.Environment {
		lines = append(lines, "ENVIRONMENT "+variable.Name+"="+variable.Value)
	}

	if len(item.SecretNames) > 0 {
		lines = append(lines, fmt.Sprintf("SECRETS     %d secret(s) redacted", len(item.SecretNames)))
	}

	if started := item.RuntimeStartedAt.Value; started != nil && *started != "" {
		lines = append(lines, "UPTIME      "+uptime(*started, now))
	}

	lines = append(lines, "CREATED     "+shared.Slice(item.CreatedAt, 10))

	return strings.Join(lines, "\n")
}

func newGetCommand() *cobra.Command {
	var opts struct {
		profile string
	}

	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one instance",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			id, err := shared.ResolveInstanceID(cmd.Context(), client, args[0])
			if err != nil {
				return err
			}

			instance, err := client.GetInstance(cmd.Context(), id)

			if err != nil {
				return err
			}

			if lastError := instance.LastError.Value; lastError != nil && *lastError != "" {
				ctx.Warn(*lastError)
			}

			ctx.Output(instance, func() output.Result {
				return output.Result{Text: describeInstance(instance, ctx.Now())}
			})

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Description: "instance name or short id", Complete: shared.Instances})
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
