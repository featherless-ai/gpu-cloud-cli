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

1. Bump `version` in `package.json` and merge.
2. Push a `v<version>` tag on `main`. Only Maintainers can.
3. The CI `publish` job checks the tag matches the version and publishes to npm.
