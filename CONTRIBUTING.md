# Contributing

```sh
pnpm install
pnpm lint && pnpm typecheck && pnpm test
pnpm build                     # dist/index.js
pnpm dev instance list         # run from source
```

## Pointing at another environment

`FEATHERLESS_CLOUD_API_URL` and `FEATHERLESS_CLOUD_CONSOLE_URL` override the production API and
console. `login` stores the API address in the profile, so log in again after changing it.

## API responses

Every response is checked against the Zod schemas in `src/types/`. When an API response changes
shape, update the matching schema here.

## Releases

Bump `version` in `package.json` in the MR that should ship. When it merges, the `publish` job on
`main` publishes that version to npm and tags `v<version>`. Merges without a bump publish nothing.
