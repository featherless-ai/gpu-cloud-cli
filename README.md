# featherless-cloud

Command-line client for [Featherless GPU Cloud](https://cloud.featherless.ai).

## Install

Requires Node.js 20 or newer.

```sh
npm install --global @featherlessai/gpu-cloud-cli
featherless-cloud --help
```

Or run it once without installing: `npx @featherlessai/gpu-cloud-cli instance list`.
`featherless-cloud upgrade` installs the latest version with npm.

## Sign in

Create an API key in the console (Credentials → API keys, scopes
`instances:read instances:write users:read`), then:

```sh
featherless-cloud login            # paste the key when prompted
featherless-cloud login --key flkey_… --profile work
```

Credentials live in `~/.config/featherless-cloud.yaml`.

## Commands

| Command | Purpose |
| --- | --- |
| `instance list` | List instances |
| `instance get <id>` | One instance: state, compute, cost, mounts |
| `instance create` | Launch an instance from a template or image |
| `instance update <id>` | Rename, change public ports (`--port`, applied at once), or reconfigure a stopped instance |
| `instance start/stop/restart <id>` | Lifecycle actions |
| `instance delete <id>` | Delete (`--yes` skips the prompt) |
| `instance logs <id> [--follow]` | Container logs |
| `instance history <id>` | Lifecycle events |
| `instance shell <id>` | Interactive web-terminal shell |
| `instance ssh <id> [-i <key>]` | Native SSH shell |
| `instance tmux <id> [name] [-i <key>]` | Create or attach a tmux session (default `default`, the session the browser terminal opens) |
| `runjob list/get <id>` | List Run Jobs or inspect one Run Job |
| `exec [options] -- …` | Run a command on temporary CPU or GPU compute |
| `runjob logs <id> [--follow]` | Live or retained Run Job logs |
| `runjob cancel/delete <id>` | Stop a Run Job or delete its record |
| `volume get/create/update/delete` | Network storage volumes |
| `volume ls [vol://<volume>/<path>]` | List volumes, or a folder in one |
| `volume find vol://<volume>/<path>` | Find files and folders below a folder by name, type, depth, size or age |
| `volume cp <sources…> <destination>` | Copy between this computer and a volume, or between volumes (`-r` for folders); resumes when run again |
| `volume sync <local-folder> vol://<volume>/<path>` | Sync a folder with a volume folder each way, sending what changed since the last sync (`--mode upload|download`, `--force`, `--include`/`--exclude`); never deletes |
| `volume mv <sources…> <destination>` | Move inside a volume, between volumes, or between this computer and a volume |
| `volume mkdir/rm vol://…` | Create folders, delete files or folders |
| `pricing list [--all]` | Current compute/storage prices |
| `billing settings` | Balance and auto-reload state |
| `billing history` | Charge and top-up history |
| `login` / `logout` / `upgrade` | Auth and upgrade |
| `completion bash\|zsh` | Print the shell completion script |

Ids accept the 10-character short id or the uuid.

## SSH and persistent terminals

```sh
featherless-cloud instance ssh <id> -i ~/.ssh/id_ed25519 --profile work
featherless-cloud instance tmux <id>                 # default
featherless-cloud instance tmux <id> training
```

These commands resolve the instance with your API profile, then run your computer's
OpenSSH client. Install `ssh` locally and register your public key in the console
under Credentials → SSH keys first. Native SSH configuration, agent identities,
and host-key verification remain in effect; `-i` selects a private key file.
No keys are registered and no software is installed automatically. `--json` is
not supported; `instance shell` remains the separate web-terminal alternative.

Instances from custom images and current templates come with `tmux`. SSH logins without a command
open a plain shell unless the instance's Auto tmux setting is "SSH logins" (`autoTmux: ssh-only`);
then they open the same `default` session as `instance tmux` and the browser terminal.
Sessions use the SSH login user's default tmux socket, not a private per-command
socket; existing tmux configuration and sessions are shared. Names are exact,
case-sensitive, 1–64 letters, digits, underscores or hyphens, starting with a
letter or digit. A session named `train` does not attach to `training`.
The browser picker sees these sessions only when its terminal runs as the same
user with the same default socket. Custom images using a different browser
terminal user or custom tmux sockets can show a different session list.

Detach with **Ctrl-b d** (default tmux binding), then reconnect with the same
command and name. Jobs in tmux survive a dropped SSH connection, but not an
instance stop, restart, replacement, or tmux server exit. Save checkpoints to a
network volume; tmux is not a job scheduler or a storage guarantee. In a plain
SSH shell, `exit` ends the shell. The normal SSH escape **Enter, then `~.`**
disconnects SSH (without terminating a running tmux session). SSH/remote exit
codes are preserved; signal termination uses the conventional `128 + signal`.

## Output modes

- Default: interactive prompts, spinners, tables.
- `--json`: machine-readable JSON on stdout (errors included); never prompts.
- `--non-interactive`: plain text, no prompts; destructive commands require `--yes`.
- `--profile <name>`: pick credentials when several accounts are stored.

## Examples

```sh
featherless-cloud instance create --name my-run --template pytorch \
  --gpu-type mi325x --gpu-count 1 --volume data:/workspace --env HF_TOKEN=…
featherless-cloud instance create --name vllm --template vllm --port 8000   # https://<id>-8000.<domain>
featherless-cloud instance update vllm --port 8000 --port 7860              # replaces every public port; --port none removes them

featherless-cloud volume create datasets --gb 500 --wait
featherless-cloud volume cp -r --update ./shards vol://datasets/train/
featherless-cloud volume cp -r vol://datasets/checkpoints/step-1000 ./ckpt
featherless-cloud volume cp -r vol://datasets/checkpoints/step-1000 vol://archive/
featherless-cloud volume find vol://datasets --name '*.safetensors' --size +1G -l -h
featherless-cloud volume find vol://datasets/tmp --older-than 30d --type f \
  | xargs featherless-cloud volume rm --yes
featherless-cloud volume sync --dry-run ./weights vol://datasets/models
featherless-cloud volume sync --mode download --mkdirs . vol://datasets/models
featherless-cloud instance shell <id>
featherless-cloud instance logs <id> --follow
featherless-cloud exec --name preprocess --cpu-cores 2 \
  -- /bin/sh -c 'echo ready; ./preprocess.sh'
featherless-cloud runjob logs <id> --follow
featherless-cloud billing settings --json | jq -r .balanceMicros
```

`instance create` is retry-safe: re-running the same command within the
idempotency window returns the original instance instead of launching twice.
`exec` uses the default PyTorch environment when neither `--template` nor
`--image` is provided. It accepts `--idempotency-key` when callers need
retry-safe execution across separate CLI invocations. Local files are not
uploaded: command files must exist in the selected image or on a network
volume mounted with `--volume <name>:<path>`.

Files in a volume are written `vol://<volume>/<path>`; every other path is on this computer, and
`cp` and `mv` copy toward whichever side the destination is. As with `cp`, a destination that ends
in `/` or is an existing folder receives each source under its own name; otherwise the one source
takes the destination's name. `mv` inside a volume renames in place. Between this computer and a
volume it copies, then deletes each source file once its own copy finished. Between two volumes
the file service copies directly, without passing through this computer. Volume names can't
contain `/`.

Uploads keep each file's modification time and refuse to replace a file unless given
`--overwrite`; `--update` replaces only files whose size or modification time differ. A file
over 16 MiB is sent as four parts at once, which the service joins into the file. A large
upload interrupted by a dropped connection continues from what arrived, and one interrupted by
stopping the command continues when the same command runs again within a day. Downloads write to
`<name>.<version>.partial` and resume from it; the file appears under its own name only once
whole. `--concurrency` (1 to 32, default 16) sets how many files move at once.

The file commands print like filesystem commands, in every output mode except `--json`: no
spinners, results on stdout, problems on stderr, nothing on success, and exit code 1 when any file
failed. `ls` prints lines like `aws s3 ls`: the local modification time, the size in bytes
(blank for folders; `-h` for 1.5 KiB, 100.0 MiB), and the name, with folders ending in `/`. A
folder listing starts with `./` and, below the volume root, `../`, and always includes hidden
files. `-r` lists everything below by path from the volume root, and `--summarize` ends with the
file count and total size. `ls` with no path prints each volume's creation time, capacity and name.
`find` searches on the file service and prints each match's `vol://` address as it is found, in
path order, so the output feeds `rm`, `cp` and `mv`; `-0` separates them with NULs for
`xargs -0`, and `-l` prints them like `ls` once the search finished. `--name` and `--iname` take a
pattern on the name (`*`, `?`, `[abc]`), `--size +N`/`-N` means more or less than N (K, M, G, T
are 1024-based) and leaves out folders, and `--newer-than`/`--older-than` take ages like `12h` or
`7d`. A search that stops before it finished exits 1 rather than print a partial list as whole.
`cp -v` and `mv -v` print each file as it finishes, and `--progress`
shows a running total on stderr. `rm` asks for confirmation only at a terminal; otherwise it
needs `--yes`.

`sync` mirrors a local folder with a volume folder: it lists each folder on both sides — one
paged request per remote folder — and remembers, in `~/.config/featherless-cloud-sync/`, which
files it left the same on both sides. The side that changed a file since then wins, whatever the
clocks say; a file changed on both sides is a conflict, and a file deleted on one side stays
deleted rather than coming back (`cp` fetches or sends it again). For a file sync has no memory
of, the newer modification time wins, and copies with the same millisecond but different sizes
are a conflict. On a conflict `sync` reports it and exits 1 rather than
guess; `--mode upload` or `--mode download` with `--force` names the side that wins. A remote
file that changes after the listing that planned its upload fails with a conflict rather than
replace it, and a local file that appears or changes while its download was planned is left
alone. `--mkdirs` creates missing folders on either side (including empty ones), `--include`
and `--exclude` take relative-path globs (`*`, `**`, `?`; a pattern without a slash, like
`*.log` or `node_modules`, matches at any depth, as in `.gitignore`; `node_modules` or
`**/node_modules` skips those whole folders, `logs/*` only the direct children of `logs`),
`--dry-run` shows the plan, and `--concurrency` (1 to 32, default 16) sets how many files move at
once while the walk continues. Sync never deletes anything, on either side.

`runjob` is the canonical command; `job` remains a compatibility alias.

## Shell completion

Bash:

```sh
echo 'eval "$(featherless-cloud completion bash)"' >> ~/.bashrc
```

Zsh (any zsh with `compinit`, the default):

```sh
mkdir -p ~/.zsh/completions
featherless-cloud completion zsh > ~/.zsh/completions/_featherless-cloud
echo 'fpath=(~/.zsh/completions $fpath)' >> ~/.zshrc
```

Open a new shell afterwards. Tab completes commands, subcommands and flags, and the values
they take: instance names and short ids, Run Job short ids and attempts, volume names,
volume and folder names inside `vol://…` paths (type `vol` + Tab for the scheme itself),
`--volume <name>:` mounts, templates, registry credentials, the GPU models, GPU counts and
CPU sizes on offer, fixed choices such as `--compute-type`, and profile names. Names
containing spaces complete with backslashes, exactly like the shell's own filename
completion, and local paths complete via the shell's built-in filename completion.

## Development

```sh
pnpm install
pnpm lint && pnpm typecheck && pnpm test
pnpm build                     # dist/index.js
pnpm dev instance list         # run from source
```

`FEATHERLESS_CLOUD_API_URL` and `FEATHERLESS_CLOUD_CONSOLE_URL` point the CLI at another API and
console; `login` stores the API address in the profile. API responses are validated with the Zod
schemas in `src/types/`, so a response shape change needs a matching update here.

Releases: bump `version` in `package.json`, merge, then push a `v<version>` tag; the CI `publish`
job releases it to npm.
