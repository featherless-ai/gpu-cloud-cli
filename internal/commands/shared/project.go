package shared

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/project"
)

// ProjectOptions are --config-file and --no-config-file.
type ProjectOptions struct {
	ConfigFile   string
	NoConfigFile bool
}

// ProjectFlags adds the flags that point at, or away from, a project file.
func ProjectFlags(cmd *cobra.Command, opts *ProjectOptions) {
	Option(cmd, &opts.ConfigFile, "--config-file <path>", "", "project defaults file (default: .featherless-cloud.toml in this folder or a parent)")
	Switch(cmd, &opts.NoConfigFile, "--no-config-file", "ignore .featherless-cloud.toml")
}

// ApplyProject fills the flags that were not typed from the project file, so
// the command then treats them as if they had been. A typed template and the
// file are alternatives: the template wins and the file is not read.
func ApplyProject(cmd *cobra.Command, opts ProjectOptions, block project.Block) error {
	flags := cmd.Flags()

	if flags.Changed("config-file") && flags.Changed("no-config-file") {
		return errs.New("Choose either --config-file or --no-config-file.")
	}

	usesTemplate := flags.Changed("template") || flags.Changed("template-id")

	if usesTemplate {
		if flags.Changed("config-file") {
			return errs.New("Choose either --config-file or --template; a template and a project file both set defaults.")
		}

		return nil
	}

	if flags.Changed("no-config-file") {
		return nil
	}

	path, err := projectPath(flags, opts)

	if err != nil {
		return err
	}

	if path == "" {
		return nil
	}

	file, err := project.Load(path)

	if err != nil {
		return err
	}

	if err := applyProjectFile(cmd, file, block); err != nil {
		return err
	}

	if contributes(file, block) {
		fmt.Fprintln(cmd.ErrOrStderr(), "Using defaults from "+project.Display(path))
	}

	return nil
}

// contributes reports whether the file sets anything the block's command uses,
// so a freshly written skeleton, every setting commented, says nothing.
func contributes(file *project.File, block project.Block) bool {
	if file.Image != "" || file.Registry != "" || file.GPUType != "" ||
		file.GPUCount != nil || file.CPUCores != nil || file.Disk != nil ||
		len(file.Volumes) > 0 || len(file.Env) > 0 {
		return true
	}

	if block == project.BlockRunJob {
		return file.Workdir != "" || file.RunJob != nil
	}

	return file.Instance != nil
}

// projectPath is --config-file, or the file found above the current folder.
func projectPath(flags *pflag.FlagSet, opts ProjectOptions) (string, error) {
	if flags.Changed("config-file") {
		if _, err := os.Stat(opts.ConfigFile); err != nil {
			return "", errs.Newf("No project file at %s.", opts.ConfigFile)
		}

		return opts.ConfigFile, nil
	}

	cwd, err := os.Getwd()

	if err != nil {
		return "", errs.Newf("Cannot read the current folder: %s", err)
	}

	return project.Find(cwd)
}

func applyProjectFile(cmd *cobra.Command, file *project.File, block project.Block) error {
	flags := cmd.Flags()

	if err := fill(flags, "image", file.Image); err != nil {
		return err
	}

	// The registry credential only matters for an image the file sets.
	if !flags.Changed("image") {
		if err := fill(flags, "registry", file.Registry); err != nil {
			return err
		}
	}

	// Any typed compute flag replaces the file's choice, not just one field.
	if !ComputeFlagsGiven(cmd) {
		if err := fillNumber(flags, "gpu-count", file.GPUCount); err != nil {
			return err
		}

		if err := fill(flags, "gpu-type", file.GPUType); err != nil {
			return err
		}

		if err := fillNumber(flags, "cpu-cores", file.CPUCores); err != nil {
			return err
		}

		if file.CPUCores != nil && block == project.BlockInstance {
			if err := fill(flags, "compute-type", string(api.ComputeCPU)); err != nil {
				return err
			}
		}
	}

	if err := fillNumber(flags, "disk", file.Disk); err != nil {
		return err
	}

	if err := fill(flags, "workdir", file.Workdir); err != nil {
		return err
	}

	if file.RunJob != nil && block == project.BlockRunJob {
		if err := fillNumber(flags, "timeout", file.RunJob.Timeout); err != nil {
			return err
		}

		if err := fillNumber(flags, "preemption-retries", file.RunJob.PreemptionRetries); err != nil {
			return err
		}

		if file.RunJob.RetainLogs != nil && !*file.RunJob.RetainLogs {
			if err := fill(flags, "no-retain-logs", "true"); err != nil {
				return err
			}
		}
	}

	if file.Instance != nil && block == project.BlockInstance {
		if err := fill(flags, "name", file.Instance.Name); err != nil {
			return err
		}

		if err := fill(flags, "command", file.Instance.Command); err != nil {
			return err
		}

		ports := make([]string, len(file.Instance.Ports))

		for i, port := range file.Instance.Ports {
			ports[i] = fmt.Sprint(port)
		}

		if err := mergeList(flags, "port", ports, false); err != nil {
			return err
		}
	}

	entries := make([]string, len(file.Volumes))

	for i, volume := range file.Volumes {
		entries[i] = volume.Name + ":" + volume.Path
	}

	if err := mergeList(flags, "volume", entries, true); err != nil {
		return err
	}

	variables := make([]string, 0, len(file.Env))

	for _, name := range file.EnvKeys() {
		variables = append(variables, name+"="+file.Env[name])
	}

	return mergeList(flags, "env", variables, false)
}

// fill sets flag to value when the flag exists and was not typed.
func fill(flags *pflag.FlagSet, name, value string) error {
	if value == "" || flags.Lookup(name) == nil || flags.Changed(name) {
		return nil
	}

	return flags.Set(name, value)
}

// fillNumber is fill for a number the file set.
func fillNumber(flags *pflag.FlagSet, name string, value *int) error {
	if value == nil {
		return nil
	}

	return fill(flags, name, fmt.Sprint(*value))
}

// mergeList puts the file's entries in front of the typed ones. A typed entry
// replaces the file entry it names: the mount path for volumes, the variable
// for env and ports.
func mergeList(flags *pflag.FlagSet, name string, entries []string, replacesByMountPath bool) error {
	if len(entries) == 0 {
		return nil
	}

	flag := flags.Lookup(name)

	if flag == nil {
		return nil
	}

	list, ok := flag.Value.(pflag.SliceValue)

	if !ok {
		return nil
	}

	typed := list.GetSlice()
	taken := map[string]bool{}

	for _, entry := range typed {
		taken[listKey(entry, replacesByMountPath)] = true
	}

	merged := make([]string, 0, len(entries)+len(typed))

	for _, entry := range entries {
		if !taken[listKey(entry, replacesByMountPath)] {
			merged = append(merged, entry)
		}
	}

	merged = append(merged, typed...)

	if err := list.Replace(merged); err != nil {
		return err
	}

	flag.Changed = true

	return nil
}

// listKey is what a typed entry replaces: the mount path of --volume PATH for
// volumes, the K of --env K=V, or the entry itself.
func listKey(entry string, isVolume bool) string {
	if isVolume {
		if separator := strings.LastIndex(entry, ":"); separator >= 0 {
			return entry[separator+1:]
		}

		return entry
	}

	if separator := strings.Index(entry, "="); separator >= 0 {
		return entry[:separator]
	}

	return entry
}
