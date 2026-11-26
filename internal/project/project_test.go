package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindStopsAtTheGitRoot(t *testing.T) {
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, FileName), "disk = 50\n")

	root := filepath.Join(outside, "gitrepo")
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")

	project := filepath.Join(root, "src", "app")
	writeFile(t, filepath.Join(project, FileName), "disk = 30\n")

	found, err := Find(project)

	if err != nil {
		t.Fatal(err)
	}

	if found != filepath.Join(project, FileName) {
		t.Errorf("got %q, want the file in the project folder", found)
	}

	withoutFile := filepath.Join(root, "src", "lib")

	if err := os.MkdirAll(withoutFile, 0o755); err != nil {
		t.Fatal(err)
	}

	found, err = Find(withoutFile)

	if err != nil {
		t.Fatal(err)
	}

	if found != "" {
		t.Errorf("got %q, want the walk to stop at the git root", found)
	}
}

func TestFindLooksInAParentFolder(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, FileName)
	writeFile(t, file, "disk = 50\n")
	writeFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")

	child := filepath.Join(root, "a", "b")

	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}

	found, err := Find(child)

	if err != nil {
		t.Fatal(err)
	}

	if found != file {
		t.Errorf("got %q, want %q", found, file)
	}
}

func TestLoadRejectsAnUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	writeFile(t, path, "disk = 50\nvolumez = 1\n")

	_, err := Load(path)

	if err == nil || !strings.Contains(err.Error(), `unknown key "volumez"`) {
		t.Errorf("got %v, want an unknown key error", err)
	}
}

func TestLoadReadsEveryKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	writeFile(t, path, `
image = "ghcr.io/org/app:1.2"
gpu_type = "MI325X"
gpu_count = 1
disk = 50
workdir = "/workspace/project"

[[volumes]]
name = "datasets"
path = "/workspace/data"

[env]
HF_HOME = "/workspace/data/hf"

[runjob]
timeout = 86400
preemption_retries = 2

[instance]
name = "dev"
ports = [8888]
`)

	file, err := Load(path)

	if err != nil {
		t.Fatal(err)
	}

	if file.Image != "ghcr.io/org/app:1.2" || file.GPUType != "MI325X" {
		t.Errorf("scalars not read: %+v", file)
	}

	if file.GPUCount == nil || *file.GPUCount != 1 || file.Disk == nil || *file.Disk != 50 {
		t.Errorf("numbers not read: %+v", file)
	}

	if len(file.Volumes) != 1 || file.Volumes[0].Name != "datasets" || file.Volumes[0].Path != "/workspace/data" {
		t.Errorf("volumes not read: %+v", file.Volumes)
	}

	if file.Env["HF_HOME"] != "/workspace/data/hf" {
		t.Errorf("env not read: %+v", file.Env)
	}

	if file.RunJob == nil || *file.RunJob.Timeout != 86400 || *file.RunJob.PreemptionRetries != 2 {
		t.Errorf("runjob not read: %+v", file.RunJob)
	}

	if file.Instance == nil || file.Instance.Name != "dev" || len(file.Instance.Ports) != 1 || file.Instance.Ports[0] != 8888 {
		t.Errorf("instance not read: %+v", file.Instance)
	}
}

func TestCheckRejectsASecretLookingEnvName(t *testing.T) {
	file := &File{Env: map[string]string{"HF_API_TOKEN": "x"}}

	if err := Check(file); err == nil || !strings.Contains(err.Error(), "env.HF_API_TOKEN looks like a secret") {
		t.Errorf("got %v, want a secret error", err)
	}
}

func TestCheckRejectsCPUCoresWithGPUs(t *testing.T) {
	gpus := 1
	cores := 8
	file := &File{CPUCores: &cores, GPUCount: &gpus}

	if err := Check(file); err == nil || !strings.Contains(err.Error(), "cpu_cores cannot be combined") {
		t.Errorf("got %v, want the compute conflict", err)
	}
}

func TestSaveRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	count := 2
	disk := 50

	if err := Save(path, &File{GPUCount: &count, Disk: &disk, Env: map[string]string{"HF_HOME": "/data"}}); err != nil {
		t.Fatal(err)
	}

	file, err := Load(path)

	if err != nil {
		t.Fatal(err)
	}

	if *file.GPUCount != 2 || *file.Disk != 50 || file.Env["HF_HOME"] != "/data" {
		t.Errorf("round trip lost values: %+v", file)
	}
}

func TestSkeletonLoadsAndSetsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)

	if err := WriteSkeleton(path); err != nil {
		t.Fatal(err)
	}

	file, err := Load(path)

	if err != nil {
		t.Fatalf("the skeleton does not load: %v", err)
	}

	if file.Image != "" || file.Registry != "" || file.GPUType != "" || file.GPUCount != nil ||
		file.CPUCores != nil || file.Disk != nil || file.Workdir != "" ||
		len(file.Volumes) > 0 || len(file.Env) > 0 || file.RunJob != nil || file.Instance != nil {
		t.Errorf("the skeleton sets values: %+v", file)
	}
}

func TestSkeletonNamesEverySetting(t *testing.T) {
	for _, key := range []string{
		"image", "registry", "gpu_type", "gpu_count", "cpu_cores", "disk", "workdir",
		"volumes", "name", "path", "env", "runjob", "timeout", "preemption_retries",
		"retain_logs", "instance", "command", "ports",
	} {
		if !strings.Contains(Skeleton, key) {
			t.Errorf("the skeleton does not mention %q", key)
		}
	}
}
