package sshkeys

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
)

var (
	defaultKeyFiles = []string{"id_ed25519.pub", "id_ecdsa.pub", "id_rsa.pub"}
	keyTypePattern  = regexp.MustCompile(`^(ssh-|ecdsa-|sk-)`)
)

// parsePublicKey checks an OpenSSH public key line and returns it with its comment.
func parsePublicKey(content string) (publicKey, comment string, err error) {
	line := strings.TrimSpace(content)

	if strings.Contains(line, "PRIVATE KEY") {
		return "", "", errs.New("That is a private key. Pass the matching .pub file instead.")
	}

	fields := strings.Fields(line)
	isPublicKey := len(fields) >= 2 && !strings.Contains(line, "\n") && keyTypePattern.MatchString(fields[0])

	if !isPublicKey {
		return "", "", errs.New("That is not an OpenSSH public key. Pass a .pub file, e.g. ~/.ssh/id_ed25519.pub.")
	}

	return line, strings.Join(fields[2:], " "), nil
}

func homeDir() string {
	if home := os.Getenv("HOME"); home != "" {
		return home
	}

	home, _ := os.UserHomeDir()

	return home
}

func findDefaultKeyFile() (string, error) {
	directory := filepath.Join(homeDir(), ".ssh")

	for _, name := range defaultKeyFiles {
		file := filepath.Join(directory, name)

		if _, err := os.Stat(file); err == nil {
			return file, nil
		}
	}

	return "", errs.New("No public key found in ~/.ssh. Pass a .pub file, or create a key with ssh-keygen -t ed25519.")
}

func newAddCommand() *cobra.Command {
	var opts struct {
		name    string
		profile string
	}

	cmd := &cobra.Command{
		Use:   "add [file]",
		Short: "Add an SSH public key for instance access",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)
			var keyFile string

			if len(args) > 0 {
				keyFile = args[0]
			} else {
				found, err := findDefaultKeyFile()
				if err != nil {
					return err
				}

				keyFile = found
			}

			content, err := os.ReadFile(keyFile)
			if err != nil {
				return errs.Newf("Could not read %s.", keyFile)
			}

			publicKey, comment, err := parsePublicKey(string(content))
			if err != nil {
				return err
			}

			name := strings.TrimSpace(opts.name)

			if name == "" {
				name = comment
			}

			if name == "" {
				name, _ = os.Hostname()
			}

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			key, err := output.Spin(ctx, "Adding SSH key", func() (api.SSHKey, error) {
				return client.AddSSHKey(cmd.Context(), api.AddSSHKeyBody{Name: name, PublicKey: publicKey})
			})

			if err != nil {
				return err
			}

			ctx.Success("Added SSH key " + key.Name + " (" + key.Fingerprint + ").")
			ctx.Output(key, func() output.Result { return output.Result{Text: key.ID} })

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "file", Description: "public key file (default: ~/.ssh/id_ed25519.pub, id_ecdsa.pub or id_rsa.pub)"})
	shared.Option(cmd, &opts.name, "--name <name>", "", "key name (default: the key comment, or this computer's name)")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
