// Package project reads the defaults a project keeps in .featherless-cloud.toml,
// so exec and instance create do not need the same flags every time.
package project

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"featherless.ai/featherless-cloud/internal/errs"
)

// FileName is the project defaults file, found in the project folder or above.
const FileName = ".featherless-cloud.toml"

// Skeleton is the file init writes: every setting, commented, with the value
// the CLI uses. Uncommenting a line is how a project changes it.
const Skeleton = `# Defaults for featherless-cloud, read by exec and instance create.
# Every setting is listed with the value the CLI uses. Uncomment one and change
# it; flags on the command line still win. The CLI finds this file in the
# current folder or a parent, up to the git root.

# Container image. Without one, exec runs the PyTorch environment.
# image = "featherlesscloud/pytorch:2.4.0"   # or one from featherless-cloud templates list

# Registry credential for a private image, by name.
# registry = "ghcr-key"

# Compute: a GPU model and count, or CPU cores. Not both.
# gpu_type = "MI325X"   # a model from: featherless-cloud gpu list
# gpu_count = 1         # 1, 2, 4 or 8
# cpu_cores = 8

# Container disk, 20-2000 GB.
# disk = 20

# Working directory in the container. exec only.
# workdir = "/workspace"

# Network volumes of your organization, mounted at a path. Up to 8.
# [[volumes]]
# name = "datasets"
# path = "/workspace/data"

# Environment variables. Secrets do not belong here: pass them with --secret.
# [env]
# HF_HOME = "/workspace/data/hf"

# Run Job settings. exec only.
# [runjob]
# timeout = 86400           # seconds, 1-86400
# preemption_retries = 0     # 0-3
# retain_logs = true

# Instance settings. instance create only.
# [instance]
# name = "dev"
# command = "jupyter lab"
# ports = [8888]
`

// WriteSkeleton writes Skeleton at path, replacing what is there.
func WriteSkeleton(path string) error {
	if err := os.WriteFile(path, []byte(Skeleton), 0o644); err != nil {
		return errs.Newf("Cannot write %s: %s", path, err)
	}

	return nil
}

// Block is the command kind a runjob or instance table applies to.
type Block int

const (
	// BlockRunJob applies the [runjob] table, for exec.
	BlockRunJob Block = iota
	// BlockInstance applies the [instance] table, for instance create.
	BlockInstance
)

// Volume mounts a network volume of the organization at a path.
type Volume struct {
	Name string `toml:"name"`
	Path string `toml:"path"`
}

// RunJob is the [runjob] table: settings only exec uses.
type RunJob struct {
	Timeout           *int  `toml:"timeout,omitempty"`
	PreemptionRetries *int  `toml:"preemption_retries,omitempty"`
	RetainLogs        *bool `toml:"retain_logs,omitempty"`
}

// Instance is the [instance] table: settings only instance create uses.
type Instance struct {
	Name    string `toml:"name,omitempty"`
	Command string `toml:"command,omitempty"`
	Ports   []int  `toml:"ports,omitempty"`
}

// File is .featherless-cloud.toml; a nil or empty field means "not set".
type File struct {
	Image    string            `toml:"image,omitempty"`
	Registry string            `toml:"registry,omitempty"`
	GPUType  string            `toml:"gpu_type,omitempty"`
	GPUCount *int              `toml:"gpu_count,omitempty"`
	CPUCores *int              `toml:"cpu_cores,omitempty"`
	Disk     *int              `toml:"disk,omitempty"`
	Workdir  string            `toml:"workdir,omitempty"`
	Volumes  []Volume          `toml:"volumes,omitempty"`
	Env      map[string]string `toml:"env,omitempty"`
	RunJob   *RunJob           `toml:"runjob,omitempty"`
	Instance *Instance         `toml:"instance,omitempty"`
}

// secretLike are the env names the file may not hold values for.
var secretLike = regexp.MustCompile(`(?i)(secret|token|password|passwd|api_?key|private_?key|credential)`)

// LooksSecret reports whether an environment variable name looks like a secret,
// which belongs in --secret or a registry key instead of the file.
func LooksSecret(name string) bool {
	return secretLike.MatchString(name)
}

// Find returns the project file at start or the nearest folder above it, and ""
// when there is none. The walk stops after the folder holding .git, the home
// folder, or the filesystem root.
func Find(start string) (string, error) {
	home, _ := os.UserHomeDir()
	dir, err := filepath.Abs(start)

	if err != nil {
		return "", errs.Newf("Cannot read %s: %s", start, err)
	}

	for {
		candidate := filepath.Join(dir, FileName)

		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}

		_, gitErr := os.Stat(filepath.Join(dir, ".git"))

		if gitErr == nil || dir == home {
			return "", nil
		}

		parent := filepath.Dir(dir)

		if parent == dir {
			return "", nil
		}

		dir = parent
	}
}

// Load reads the file at path and checks it.
func Load(path string) (*File, error) {
	var file File
	meta, err := toml.DecodeFile(path, &file)

	if err != nil {
		return nil, errs.Newf("%s: %s", path, err)
	}

	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return nil, errs.Newf("%s: unknown key %q.", path, undecoded[0].String())
	}

	if err := Check(&file); err != nil {
		return nil, errs.Newf("%s: %s", path, err)
	}

	return &file, nil
}

// Check verifies the file's keys without asking the API: ranges, the compute
// choice, mount paths, and that no secret sits in env.
func Check(file *File) error {
	if file.Disk != nil && (*file.Disk < 20 || *file.Disk > 2000) {
		return errs.New("disk must be between 20 and 2000 GB.")
	}

	if file.GPUCount != nil && *file.GPUCount != 1 && *file.GPUCount != 2 && *file.GPUCount != 4 && *file.GPUCount != 8 {
		return errs.New("gpu_count must be 1, 2, 4 or 8.")
	}

	if file.CPUCores != nil && *file.CPUCores < 1 {
		return errs.New("cpu_cores must be at least 1.")
	}

	if file.CPUCores != nil && (file.GPUType != "" || file.GPUCount != nil) {
		return errs.New("cpu_cores cannot be combined with gpu_type or gpu_count.")
	}

	if file.RunJob != nil {
		if timeout := file.RunJob.Timeout; timeout != nil && (*timeout < 1 || *timeout > 86_400) {
			return errs.New("runjob.timeout must be between 1 and 86400.")
		}

		if retries := file.RunJob.PreemptionRetries; retries != nil && (*retries < 0 || *retries > 3) {
			return errs.New("runjob.preemption_retries must be between 0 and 3.")
		}
	}

	if file.Instance != nil {
		for _, port := range file.Instance.Ports {
			if port < 1 || port > 65_535 {
				return errs.New("instance.ports must be between 1 and 65535.")
			}
		}
	}

	paths := map[string]bool{}

	for _, volume := range file.Volumes {
		if strings.TrimSpace(volume.Name) == "" {
			return errs.New("Each [[volumes]] entry needs a name.")
		}

		if !strings.HasPrefix(volume.Path, "/") {
			return errs.Newf("Volume \"%s\" needs an absolute path, got \"%s\".", volume.Name, volume.Path)
		}

		if paths[volume.Path] {
			return errs.Newf("Two volumes mount at %s. Use one mount path per volume.", volume.Path)
		}

		paths[volume.Path] = true
	}

	for _, name := range file.EnvKeys() {
		if LooksSecret(name) {
			return errs.Newf("env.%s looks like a secret. Keep secrets out of %s; pass them with --secret.", name, FileName)
		}
	}

	return nil
}

// Save writes the file at path, keeping the field order of File.
func Save(path string, file *File) error {
	var buffer bytes.Buffer

	if err := toml.NewEncoder(&buffer).Encode(file); err != nil {
		return errs.Newf("Cannot encode %s: %s", path, err)
	}

	if err := os.WriteFile(path, buffer.Bytes(), 0o644); err != nil {
		return errs.Newf("Cannot write %s: %s", path, err)
	}

	return nil
}

// Display words a path for messages: relative to this folder when it is below it.
func Display(path string) string {
	cwd, err := os.Getwd()

	if err != nil {
		return path
	}

	relative, err := filepath.Rel(cwd, path)

	if err != nil || strings.HasPrefix(relative, "..") {
		return path
	}

	return "./" + relative
}

// Keys is a file's env names in order, for callers that merge them.
func (f *File) EnvKeys() []string {
	names := make([]string, 0, len(f.Env))

	for name := range f.Env {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}
