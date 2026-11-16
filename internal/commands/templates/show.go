package templates

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/output"
)

func describeTemplate(item api.Template, all []api.Template, pricing []api.PriceRate) string {
	vendors := make([]string, len(item.GPUVendors))

	for i, vendor := range item.GPUVendors {
		vendors[i] = "NVIDIA"

		if vendor == api.VendorAMD {
			vendors[i] = "AMD"
		}
	}

	lines := []string{
		"NAME        " + shared.TemplateReference(item, all),
		"ID          " + item.ShortID,
		"VISIBILITY  " + string(item.Visibility),
		"OWNER       " + ownerName(item),
		"TAGS        " + orDash(strings.Join(item.Tags, ", ")),
		"GPUS        " + strings.Join(vendors, ", "),
	}

	if len(item.Versions) > 0 {
		versions := make([]string, len(item.Versions))

		for i, entry := range item.Versions {
			versions[i] = entry.Version

			if i == 0 {
				versions[i] += " (current)"
			}
		}

		lines = append(lines, "VERSIONS    "+strings.Join(versions, ", "))
	}

	disk := "-"

	if item.EphemeralDiskGiB != nil && *item.EphemeralDiskGiB != 0 {
		disk = fmt.Sprintf("%d GB", *item.EphemeralDiskGiB)
	}

	environment := make([]string, len(item.Environment))

	for i, entry := range item.Environment {
		environment[i] = entry.Name + "=" + entry.Value
	}

	mounts := make([]string, len(item.NetworkVolumeMounts))

	for i, mount := range item.NetworkVolumeMounts {
		mounts[i] = mount.MountPath
	}

	ports := make([]string, len(item.PublicPorts))

	for i, port := range item.PublicPorts {
		ports[i] = strconv.Itoa(port)
	}

	lines = append(lines,
		"IMAGE       "+item.Image,
		"COMPUTE     "+computeName(pricing, item.ComputeSKU),
		"DISK        "+disk,
		"COMMAND     "+orDash(strings.Join(item.Command, " ")),
		"ENV         "+orDash(strings.Join(environment, " ")),
		"SECRETS     "+orDash(strings.Join(item.SecretNames, " ")),
		"VOLUMES     "+orDash(strings.Join(mounts, " ")),
		"PORTS       "+orDash(strings.Join(ports, " ")),
	)

	if item.Description != "" {
		lines = append(lines, "", item.Description)
	}

	return strings.Join(lines, "\n")
}

func newShowCommand() *cobra.Command {
	var opts struct {
		profile string
	}

	cmd := &cobra.Command{
		Use:   "show <template>",
		Short: "Show a template's settings",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			loaded, err := loadCatalog(cmd, client)

			if err != nil {
				return err
			}

			template, err := shared.ResolveTemplateRef(loaded.templates, args[0])
			if err != nil {
				return err
			}

			ctx.Output(template, func() output.Result {
				return output.Result{Text: describeTemplate(template, loaded.templates, loaded.pricing)}
			})

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "template", Description: "template name or id", Complete: shared.Templates})
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
