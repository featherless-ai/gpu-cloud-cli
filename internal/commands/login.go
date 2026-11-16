package commands

import (
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/browser"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/config"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
)

// keyPattern is the API's key secret format.
var keyPattern = regexp.MustCompile(`^flkey_[A-Za-z0-9_-]{43}$`)

// Seams tests replace.
var openBrowser = browser.Open

const malformedKey = "That does not look like an API key. Keys start with flkey_."

func isConnectionFailure(err error) bool {
	return errs.IsCLI(err) && strings.HasPrefix(err.Error(), "Could not connect")
}

// verifyKey confirms the secret exists. A 403 means the key is valid but
// lacks users:read; the email is then unknown, which is fine.
func verifyKey(cmd *cobra.Command, key string) (string, error) {
	client := shared.NewCloud(config.APIBaseURL(), key)
	me, err := client.GetMe(cmd.Context())

	if err == nil {
		return me.Data.User.Email, nil
	}

	if errs.IsKind(err, errs.KindForbidden) {
		return "", nil
	}

	if isConnectionFailure(err) {
		return "", err
	}

	return "", errs.New("The API rejected that key. Check it was copied in full and has not been revoked.")
}

func storeProfile(ctx *output.Context, profile, key, email string) error {
	cfg, err := shared.LoadConfig()
	if err != nil {
		return err
	}

	entry := config.Profile{
		APIBase:   config.APIBaseURL(),
		APIKey:    key,
		CreatedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		Email:     email,
	}

	cfg.Profiles.Set(profile, entry)

	if cfg.CurrentProfile == "" || len(cfg.Profiles) == 1 {
		cfg.CurrentProfile = profile
	}

	if err := config.Save(cfg); err != nil {
		return err
	}

	signedInAs := ""

	if email != "" {
		signedInAs = " as " + email
	}

	ctx.Success("Signed in" + signedInAs + " (profile \"" + profile + "\").")

	return nil
}

func signIn(cmd *cobra.Command, ctx *output.Context, profile, key string) error {
	email, err := verifyKey(cmd, key)

	if err != nil {
		return err
	}

	return storeProfile(ctx, profile, key, email)
}

func pasteKey(cmd *cobra.Command, ctx *output.Context, profile string) error {
	url := config.ConsoleBaseURL() + "/credentials/api-keys?create=1"
	ctx.Log("Create an API key in your browser, then paste it below.\n  " + url)
	openBrowser(url)

	validate := func(value string) error {
		if value == "" {
			return errs.New("Paste the key shown in the browser, or press Ctrl+C to cancel.")
		}

		if !keyPattern.MatchString(value) {
			return errs.New(malformedKey)
		}

		return nil
	}

	for {
		answer, err := ctx.Prompt.Text("API key", "flkey_…", "", validate)

		if err == output.ErrCancelled {
			return &errs.Exit{Code: 0}
		}

		if err != nil {
			return err
		}

		err = signIn(cmd, ctx, profile, strings.TrimSpace(answer))

		if err == nil || isConnectionFailure(err) {
			return err
		}

		ctx.Warn("The API rejected that key. Create a new one in the browser and paste it again.")
	}
}

func newLoginCommand() *cobra.Command {
	var opts struct {
		profile string
		key     string
	}

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Store an API key for this device",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			if key := strings.TrimSpace(opts.key); key != "" {
				if !keyPattern.MatchString(key) {
					return errs.New(malformedKey)
				}

				return signIn(cmd, ctx, opts.profile, key)
			}

			if ctx.Mode != output.Interactive {
				return errs.New("Pass the key with --key <secret>. Create one at " + config.ConsoleBaseURL() + "/credentials/api-keys.")
			}

			return pasteKey(cmd, ctx, opts.profile)
		},
	}

	shared.Option(cmd, &opts.profile, "--profile <name>", "default", "profile to store the credentials under")
	completion.Flag(cmd, "profile", shared.Profiles)
	shared.Option(cmd, &opts.key, "--key <secret>", "", "API key to store (non-interactive)")

	return cmd
}
