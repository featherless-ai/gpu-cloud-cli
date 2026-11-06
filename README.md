# Featherless GPU Cloud CLI

`featherless-cloud` launches and manages GPU instances, Run Jobs and network volumes on
[Featherless GPU Cloud](https://console.cloud.featherless.ai) from your terminal.

## Install

Requires Node.js 20 or newer.

```sh
npm install --global @featherlessai/gpu-cloud-cli
featherless-cloud --help
```

To try it without installing:

```sh
npx @featherlessai/gpu-cloud-cli pricing list
```

## Quick start

```sh
featherless-cloud login                                   # opens the console to create an API key
featherless-cloud pricing list                            # GPUs on offer and their hourly price
featherless-cloud instance create --name my-run --template pytorch --gpu-count 1
featherless-cloud instance ssh my-run
featherless-cloud instance delete my-run
```

Commands that take an instance, Run Job or volume accept its name or its 10-character short id.

## Sign in

```sh
featherless-cloud login
```

`login` opens [API keys](https://console.cloud.featherless.ai/credentials/api-keys) in your
browser. Create a key there and paste it into the terminal.

- **Scripts and CI:** `featherless-cloud login --key flkey_…`
- **Several accounts:** add `--profile work` to `login`, then to any other command.
- **Sign out:** `featherless-cloud logout`, then revoke the key in the console.

Keys are stored in `~/.config/featherless-cloud.yaml` (`%APPDATA%\featherless-cloud.yaml` on
Windows), readable only by you.

## Instances

```sh
featherless-cloud instance create --name train --template pytorch \
  --gpu-type mi325x --gpu-count 1 --volume data:/workspace --env HF_TOKEN=…
featherless-cloud instance list
featherless-cloud instance get train
featherless-cloud instance stop train
featherless-cloud instance start train
featherless-cloud instance delete train
```

- **Image:** `--template <name>` for a template (see [Templates](#templates)), or `--image <reference>`
  for your own container (add `--registry <name>` for a private one, see [Private images](#private-images)).
- **Compute:** `--gpu-type` and `--gpu-count` (1, 2, 4 or 8), or `--compute-type cpu --cpu-cores <n>`.
- **Storage:** `--disk <gb>` for the container disk; `--volume <name>:<path>` mounts a network volume.
- **Settings:** `--env K=V` and `--secret K=V`, both repeatable.
- **Retry-safe:** running the same `create` again shortly after returns the first instance instead
  of launching a second one.

### Public ports

```sh
featherless-cloud instance create --name vllm --template vllm --port 8000
featherless-cloud instance update vllm --port 8000 --port 7860    # replaces the list
featherless-cloud instance update vllm --port none                # removes them all
```

Each port gets its own public HTTPS address, shown by `instance get`. Port changes apply at once.
Other changes in `instance update` (image, disk, command, environment) need a stopped instance.

### Logs and history

```sh
featherless-cloud instance logs train --follow
featherless-cloud instance history train
```

`logs` keeps a bounded amount of recent output, for debugging. Send anything you need to keep to
your own logging service.

## Terminals

```sh
featherless-cloud instance shell train      # browser-style terminal, no SSH needed
featherless-cloud instance ssh train        # your own OpenSSH client
featherless-cloud instance tmux train       # attach to the default tmux session
featherless-cloud instance tmux train eval  # or create/attach a named one
```

`ssh` and `tmux` need `ssh` installed and your public key added:

```sh
featherless-cloud ssh-keys add                     # ~/.ssh/id_ed25519.pub (or id_ecdsa, id_rsa)
featherless-cloud ssh-keys add ~/.ssh/work.pub --name work
featherless-cloud ssh-keys list
featherless-cloud ssh-keys delete work
```

Your SSH config and agent apply as usual; `-i <file>` picks a key.

### Keeping work running with tmux

- Detach with **Ctrl-b d** and run the same command later to reconnect.
- Work in tmux survives a dropped connection, but not stopping or restarting the instance.
  Save checkpoints to a network volume.
- `tmux train` with no name opens the same session as the browser terminal.
- **Enter, then `~.`** disconnects SSH without ending the session.

## Run Jobs

A Run Job runs one command on temporary compute and stops when it finishes. You pay only while
it runs.

```sh
featherless-cloud exec --gpu-count 1 -- python train.py --epochs 3
featherless-cloud exec --cpu-cores 4 --volume data:/workspace/data -- ./preprocess.sh
featherless-cloud exec --detach --name nightly -- python eval.py
featherless-cloud runjob list
featherless-cloud runjob logs nightly --follow
featherless-cloud runjob cancel nightly
```

- Without `--template` or `--image`, `exec` uses the PyTorch environment.
- With a template that has a start command, the command after `--` is optional.
- Your local files aren't uploaded: the command and its files must be in the image or on a
  mounted volume.
- `exec` streams the output and exits with the command's exit code. **Ctrl-C** detaches;
  `--cancel-on-interrupt` cancels instead.
- `--timeout <seconds>` caps the run time (default 24 hours).
- `--idempotency-key <key>` makes a retried `exec` reuse the first Run Job.

## Templates

Save the settings you launch with, then launch by name. A template keeps the image, compute,
disk, volumes, start command, environment, secrets and ports.

```sh
featherless-cloud templates create trainer --image ghcr.io/acme/trainer:1 --registry ghcr \
  --gpu-count 1 --disk 100 --volume data:/workspace --env EPOCHS=3 --secret HF_TOKEN=… \
  --tag AMD --tag "ROCm 7.14"
featherless-cloud instance create --template trainer
featherless-cloud exec --template trainer --gpu-count 8 -- python train.py --epochs 10
featherless-cloud templates list
featherless-cloud templates update trainer --visibility organization
```

- Any flag you pass when launching replaces the template's value; `--env` and `--secret` add to or
  replace the template's by name.
- `--template` finds your template first, then one shared in your organization, then an official
  one. `org/<name>` and `featherless/<name>` pick one explicitly; `--template-id <id>` takes the ID
  from `templates list`.
- Templates are private until `--visibility organization`. Every member can then launch one, with
  its secrets, but only you or an organization admin can change it.
- `templates create --template <official>` starts from an official template's image.
- Official templates can offer several versions; `--template featherless/rocm-pytorch:2.11.0` (or
  `--template-version 2.11.0`) launches an older one, and `templates show` lists them.
- `--tag` labels a template; `templates list` shows the tags. Repeating `--tag` on `update`
  replaces the saved tags.
- Changing or deleting a template doesn't affect instances or Run Jobs already launched from it.

## Private images

Add the registry login once, then pass its name with `--registry`:

```sh
featherless-cloud registry-keys add ghcr --server ghcr.io --username octocat
echo "$GHCR_TOKEN" | featherless-cloud registry-keys add ghcr --server ghcr.io --username octocat --password-stdin
featherless-cloud registry-keys list
featherless-cloud exec --image ghcr.io/octocat/train:1 --registry ghcr -- python train.py
```

Registry keys are shared by your organization; only owners and admins can add, change or remove
them. `registry-keys update <name> --change-password` replaces the stored password.

## Volumes and files

Network volumes keep data across instances and Run Jobs.

```sh
featherless-cloud volume create datasets --gb 500 --wait
featherless-cloud volume ls
featherless-cloud volume update datasets --gb 1000
featherless-cloud volume delete datasets
```

Files inside a volume are written `vol://<volume>/<path>`. Every other path is on your computer.

```sh
featherless-cloud volume ls -h vol://datasets/train
featherless-cloud volume cp -r ./shards vol://datasets/train/
featherless-cloud volume cp -r vol://datasets/checkpoints/step-1000 ./ckpt
featherless-cloud volume mv vol://datasets/tmp/run1 vol://datasets/runs/
featherless-cloud volume mkdir vol://datasets/eval
featherless-cloud volume rm -r vol://datasets/tmp
```

### Copying

- `cp` and `mv` work in either direction, and between two volumes.
- A destination ending in `/`, or an existing folder, receives each source under its own name.
- Existing files are kept unless you pass `--overwrite`. `--update` replaces only files whose size
  or modification time differ.
- An interrupted copy resumes: run the same command again.
- `-v` prints each file; `--progress` shows a running total.
- Exit code 1 means at least one file failed; the others still copied.

### Finding files

```sh
featherless-cloud volume find vol://datasets --name '*.safetensors' --size +1G -l -h
featherless-cloud volume find vol://datasets/tmp --older-than 30d --type f \
  | xargs featherless-cloud volume rm --yes
```

- `--name` / `--iname` match names with `*`, `?` and `[abc]`.
- `--type f` or `--type d` for files or folders only; `--maxdepth <n>` limits the depth.
- `--size +1G` / `--size -512K` for bigger or smaller than (K, M, G, T).
- `--newer-than` / `--older-than` take ages like `30m`, `12h` or `7d`.
- `-l` prints sizes and dates, like `ls -l`.

### Syncing a folder

```sh
featherless-cloud volume sync --dry-run ./weights vol://datasets/models
featherless-cloud volume sync ./weights vol://datasets/models
featherless-cloud volume sync --mode download --mkdirs . vol://datasets/models
```

- `sync` sends what changed on each side since the last sync. It never deletes anything.
- A file deleted on one side stays deleted; use `cp` to bring it back.
- A file changed on both sides is a conflict: `sync` reports it and exits 1. Pick the winner with
  `--mode upload --force` or `--mode download --force`.
- `--mode upload` or `--mode download` syncs one way only.
- `--include` / `--exclude` take globs like `*.log` or `node_modules`.
- `--mkdirs` also creates missing folders, including empty ones.

## Pricing and billing

```sh
featherless-cloud pricing list
featherless-cloud pricing list --workload run-job --all
featherless-cloud billing settings
featherless-cloud billing history
```

Add credit and set up auto-reload under [Billing](https://console.cloud.featherless.ai/billing).

## Scripting

- `--json` prints machine-readable JSON on stdout, errors included, and never prompts.
- `--non-interactive` prints plain text without spinners or prompts.
- Commands that delete something need `--yes` when they can't ask.
- Non-zero exit codes mean failure; `ssh`, `tmux` and `exec` pass on the remote command's exit code.

```sh
featherless-cloud instance list --json | jq -r '.items[].name'
```

## Shell completion

Bash:

```sh
echo 'eval "$(featherless-cloud completion bash)"' >> ~/.bashrc
```

Zsh:

```sh
mkdir -p ~/.zsh/completions
featherless-cloud completion zsh > ~/.zsh/completions/_featherless-cloud
echo 'fpath=(~/.zsh/completions $fpath)' >> ~/.zshrc
```

Open a new shell afterwards. Tab then completes commands and flags, plus your instance, Run Job
and volume names, folders inside `vol://` paths, templates, GPU models and profiles.

## Command reference

| Command | What it does |
| --- | --- |
| `login`, `logout` | Store or remove an API key |
| `instance list`, `get` | List instances, or show one |
| `instance create`, `update`, `delete` | Launch, change or delete an instance |
| `instance start`, `stop`, `restart` | Start, stop or restart an instance |
| `instance logs`, `history` | Recent output; lifecycle events |
| `instance shell`, `ssh`, `tmux` | Open a terminal |
| `exec -- <command>` | Run a command as a Run Job |
| `runjob list`, `get`, `logs` | Inspect Run Jobs |
| `runjob cancel`, `delete` | Stop a Run Job, or delete its record |
| `ssh-keys list`, `add`, `delete` | Manage your SSH public keys |
| `registry-keys list`, `add`, `update`, `delete` | Manage registry logins for private images |
| `templates list`, `show` | List templates, or show one's settings |
| `templates create`, `update`, `delete` | Save, change or delete a template |
| `volume create`, `get`, `update`, `delete` | Manage network volumes |
| `volume ls`, `find` | Browse files in a volume |
| `volume cp`, `mv`, `mkdir`, `rm` | Manage files in a volume |
| `volume sync` | Sync a folder with a volume folder |
| `pricing list` | Current prices |
| `billing settings`, `history` | Balance, auto-reload and charges |
| `upgrade` | Install the latest version |
| `completion bash`, `zsh` | Print a shell completion script |

`featherless-cloud <command> --help` lists every option.
