#!/bin/sh
# Prints the Homebrew formula for a published version. It installs the same
# binaries npm serves, so Homebrew needs no hosting of its own.
# Usage: scripts/homebrew-formula.sh <version> > Formula/featherless-cloud.rb
set -eu

version=${1:?usage: homebrew-formula.sh <version>}
registry=${FEATHERLESS_CLOUD_REGISTRY:-https://registry.npmjs.org}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

url() {
  echo "$registry/@featherlessai/gpu-cloud-cli-$1/-/gpu-cloud-cli-$1-$version.tgz"
}

sha256() {
  curl -fsSL "$(url "$1")" -o "$work/$1.tgz"
  (sha256sum "$work/$1.tgz" 2>/dev/null || shasum -a 256 "$work/$1.tgz") | cut -d ' ' -f 1
}

cat <<RUBY
class FeatherlessCloud < Formula
  desc "Command-line client for Featherless GPU Cloud"
  homepage "https://console.cloud.featherless.ai"
  version "$version"

  on_macos do
    on_arm do
      url "$(url darwin-arm64)"
      sha256 "$(sha256 darwin-arm64)"
    end

    on_intel do
      url "$(url darwin-x64)"
      sha256 "$(sha256 darwin-x64)"
    end
  end

  on_linux do
    on_arm do
      url "$(url linux-arm64)"
      sha256 "$(sha256 linux-arm64)"
    end

    on_intel do
      url "$(url linux-x64)"
      sha256 "$(sha256 linux-x64)"
    end
  end

  def install
    bin.install "bin/featherless-cloud"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/featherless-cloud --version")
  end
end
RUBY
