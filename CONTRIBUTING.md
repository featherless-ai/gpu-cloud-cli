# Contributing

The CLI is Go (see `go.mod` for the version). `package.json` only carries the name and version npm
publishes; the binary reads both from it.

```sh
go test -race ./...                       # the completion tests need bash and zsh on PATH
go run ./cmd/featherless-cloud --help     # run from source
scripts/build-npm.sh --pack               # every platform's binary and npm package, in dist/npm/
git config core.hooksPath .githooks       # gofmt and go vet before each commit
```

## Pointing at another environment

`FEATHERLESS_CLOUD_API_URL` and `FEATHERLESS_CLOUD_CONSOLE_URL` override the production API and
console. `login` stores the API address in the profile, so log in again after changing it.

## API responses

Every response is checked against the types in `internal/api`, with the same required, nullable and
optional fields the API documents. When an API response changes shape, update the matching type.

## Comparing with the TypeScript CLI

`internal/parity` runs the TypeScript CLI (the last one is commit `50b4613`) and this one against
the same fake API and compares their output, exit codes, config files and requests. Build the old CLI
from that commit with `pnpm install && pnpm build`, then:

```sh
go build -o /tmp/featherless-cloud ./cmd/featherless-cloud
PARITY_TS_CLI=/path/to/old/dist/index.js PARITY_GO_CLI=/tmp/featherless-cloud go test ./internal/parity/
```

## Releases

Bump `version` in `package.json` in the MR that should ship. When it merges, the `publish` job on
`main` publishes the platform packages (`@featherlessai/gpu-cloud-cli-<os>-<arch>`) and then
`@featherlessai/gpu-cloud-cli` to npm, and tags `v<version>`. Merges without a bump publish nothing.

`scripts/homebrew-formula.sh <version>` prints a Homebrew formula for a published version.
