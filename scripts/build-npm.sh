#!/bin/sh
# Builds the binary for every platform and lays out the npm packages:
#   dist/npm/gpu-cloud-cli-<os>-<arch>/   one per platform, holding bin/featherless-cloud
#   dist/npm/gpu-cloud-cli/               the package people install, with the shim
# Usage: scripts/build-npm.sh [--pack]
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
out="$root/dist/npm"
version=$(sed -n 's/^  "version": "\(.*\)",$/\1/p' "$root/package.json")

if [ -z "$version" ]; then
  echo "No version in package.json." >&2
  exit 1
fi

rm -rf "$out"
mkdir -p "$out"
optional=""

for target in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/amd64; do
  goos=${target%/*}
  goarch=${target#*/}

  case "$goos" in
    windows) os=win32 ;;
    *) os=$goos ;;
  esac

  case "$goarch" in
    amd64) cpu=x64 ;;
    *) cpu=$goarch ;;
  esac

  name="gpu-cloud-cli-$os-$cpu"
  executable=featherless-cloud
  [ "$goos" = windows ] && executable=featherless-cloud.exe
  mkdir -p "$out/$name/bin"

  (cd "$root" && CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch \
    go build -trimpath -ldflags="-s -w" -o "$out/$name/bin/$executable" ./cmd/featherless-cloud)

  cat > "$out/$name/package.json" <<JSON
{
  "name": "@featherlessai/$name",
  "version": "$version",
  "description": "The featherless-cloud binary for $os-$cpu",
  "homepage": "https://console.cloud.featherless.ai",
  "license": "UNLICENSED",
  "os": ["$os"],
  "cpu": ["$cpu"],
  "files": ["bin"],
  "publishConfig": { "access": "public" }
}
JSON

  optional="$optional${optional:+,
}    \"@featherlessai/$name\": \"$version\""
done

main="$out/gpu-cloud-cli"
mkdir -p "$main/bin"
cp "$root/npm/featherless-cloud.cjs" "$main/bin/featherless-cloud.cjs"
cp "$root/README.md" "$main/README.md"

cat > "$main/package.json" <<JSON
{
  "name": "@featherlessai/gpu-cloud-cli",
  "version": "$version",
  "description": "Command-line client for Featherless GPU Cloud",
  "homepage": "https://console.cloud.featherless.ai",
  "license": "UNLICENSED",
  "bin": { "featherless-cloud": "bin/featherless-cloud.cjs" },
  "files": ["bin"],
  "engines": { "node": ">=18" },
  "keywords": ["featherless", "gpu", "cloud", "cli"],
  "optionalDependencies": {
$optional
  },
  "publishConfig": { "access": "public" }
}
JSON

if [ "${1:-}" = "--pack" ]; then
  for dir in "$out"/*/; do
    (cd "$dir" && npm pack --silent --pack-destination "$out" >/dev/null)
  done

  ls "$out"/*.tgz
fi
