package templates

import (
	"strings"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/output"
)

// catalog is the templates and the prices that name their compute.
type catalog struct {
	templates []api.Template
	pricing   []api.PriceRate
}

// loadCatalog fetches both, as the TypeScript CLI started them at once, and
// returns the first failure.
func loadCatalog(cmd *cobra.Command, client api.Cloud) (catalog, error) {
	templates, templatesErr := client.Templates(cmd.Context())
	pricing, pricingErr := client.Pricing(cmd.Context(), api.WorkloadInstance)

	return catalog{templates: templates, pricing: pricing}, shared.FirstError(templatesErr, pricingErr)
}

// computeName is the offer a template's compute SKU sells, or -.
func computeName(pricing []api.PriceRate, sku *string) string {
	if sku == nil {
		return "-"
	}

	for _, rate := range pricing {
		if rate.SKU == *sku {
			return rate.DisplayName
		}
	}

	return "-"
}

func ownerName(template api.Template) string {
	if template.Owner == nil {
		return "Featherless"
	}

	return template.Owner.Name
}

func orDash(text string) string {
	if text == "" {
		return "-"
	}

	return text
}

func newListCommand() *cobra.Command {
	var opts struct {
		profile string
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List official templates, yours, and those shared in your organization",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			loaded, err := output.Spin(ctx, "Fetching templates", func() (catalog, error) {
				return loadCatalog(cmd, client)
			})

			if err != nil {
				return err
			}

			list := loaded.templates

			ctx.Output(list, func() output.Result {
				rows := make([][]string, len(list))

				for i, template := range list {
					rows[i] = []string{
						shared.TemplateReference(template, list),
						string(template.Visibility),
						ownerName(template),
						orDash(strings.Join(template.Tags, ", ")),
						template.Image,
						computeName(loaded.pricing, template.ComputeSKU),
						template.ShortID,
					}
				}

				header := []string{"NAME", "VISIBILITY", "OWNER", "TAGS", "IMAGE", "COMPUTE", "ID"}

				return output.Result{Table: &output.Table{Header: header, Rows: rows}}
			})

			return nil
		},
	}

	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
