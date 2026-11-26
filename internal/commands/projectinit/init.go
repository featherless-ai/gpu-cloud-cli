// Package projectinit is the `init` command, which writes the project defaults
// file, .featherless-cloud.toml.
package projectinit

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
	"featherless.ai/featherless-cloud/internal/project"
)

// InitCommand is `featherless-cloud init`.
func InitCommand() *cobra.Command {
	var opts struct {
		profile      string
		fromInstance string
		force        bool
	}

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write .featherless-cloud.toml with this project's defaults",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, opts.profile, opts.fromInstance, opts.force)
		},
	}

	shared.Option(cmd, &opts.fromInstance, "--from-instance <name>", "", "copy the settings of an existing instance")
	completion.Flag(cmd, "from-instance", shared.Instances)
	shared.Switch(cmd, &opts.force, "--force", "overwrite an existing .featherless-cloud.toml")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use for --from-instance")
	completion.Flag(cmd, "profile", shared.Profiles)
	shared.HelpAfter(cmd, "\nThe file is meant to be committed, so everyone on the project runs the\nsame settings. Secrets do not belong in it: pass them with --secret.")

	return cmd
}

func runInit(cmd *cobra.Command, profile, fromInstance string, force bool) error {
	ctx := shared.NewContext(cmd)
	target := "./" + project.FileName

	if _, err := os.Stat(target); err == nil && !force {
		return errs.Newf("%s already exists. Edit it, or pass --force to replace it.", target)
	}

	if fromInstance == "" {
		if err := project.WriteSkeleton(target); err != nil {
			return err
		}

		ctx.Success("Wrote " + target + " with every setting commented. Uncomment what the project needs, or pass --from-instance <name> next time.")

		return nil
	}

	client, err := shared.Client(profile)

	if err != nil {
		return err
	}

	file, err := fromExistingInstance(cmd, ctx, client, fromInstance)

	if err != nil {
		return err
	}

	if err := project.Check(file); err != nil {
		return err
	}

	if err := project.Save(target, file); err != nil {
		return err
	}

	ctx.Success("Wrote " + target + ". Commit it so your team shares the setup.")

	return nil
}

// fromExistingInstance copies what an instance runs, so a project can repeat it.
func fromExistingInstance(cmd *cobra.Command, ctx *output.Context, client api.Cloud, ref string) (*project.File, error) {
	id, err := shared.ResolveInstanceID(cmd.Context(), client, ref)

	if err != nil {
		return nil, err
	}

	instance, err := client.GetInstance(cmd.Context(), id)

	if err != nil {
		return nil, err
	}

	file := &project.File{Env: map[string]string{}}

	if instance.Image != nil {
		file.Image = instance.Image.RequestedReference

		if file.Image == "" {
			file.Image = instance.Image.Reference
		}
	}

	if instance.ComputeType == api.ComputeCPU {
		if instance.Resources != nil && instance.Resources.CPUCores > 0 {
			cores := instance.Resources.CPUCores
			file.CPUCores = &cores
		}
	} else {
		if instance.Resources != nil && instance.Resources.GPUModel != nil {
			file.GPUType = *instance.Resources.GPUModel
		}

		if instance.GPUCount > 0 {
			count := instance.GPUCount
			file.GPUCount = &count
		}
	}

	if instance.EphemeralDiskGiB > 0 {
		disk := instance.EphemeralDiskGiB
		file.Disk = &disk
	}

	for _, mount := range instance.NetworkVolumeMounts {
		file.Volumes = append(file.Volumes, project.Volume{Name: mount.Volume.Name, Path: mount.MountPath})
	}

	for _, variable := range instance.Environment {
		if project.LooksSecret(variable.Name) {
			ctx.Warn("Left out env." + variable.Name + ": it looks like a secret. Pass it with --secret.")
			continue
		}

		file.Env[variable.Name] = variable.Value
	}

	instanceBlock := &project.Instance{Name: instance.Name}

	if len(instance.Command) > 0 {
		instanceBlock.Command = strings.Join(instance.Command, " ")
	}

	for _, port := range instance.PublicPorts {
		instanceBlock.Ports = append(instanceBlock.Ports, port.Port)
	}

	file.Instance = instanceBlock

	if instance.Template.Value != nil {
		ctx.Warn("Instance " + instance.Name + " runs template " + instance.Template.Value.Name + "; its image was copied instead.")
	}

	return file, nil
}
