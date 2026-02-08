#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <version-tag>"
  echo "example: $0 v0.1.1"
  exit 1
fi

VERSION_TAG="$1"
if [[ ! "$VERSION_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "error: version tag must look like v1.2.3"
  exit 1
fi

VERSION="${VERSION_TAG#v}"
URL="https://github.com/victorarias/attn/archive/refs/tags/${VERSION_TAG}.tar.gz"

TMP_FILE="$(mktemp)"
trap 'rm -f "$TMP_FILE"' EXIT

curl -fsSL "$URL" -o "$TMP_FILE"
SHA256="$(shasum -a 256 "$TMP_FILE" | awk '{print $1}')"

cat > Formula/attn.rb <<EOF
class Attn < Formula
  desc "Desktop orchestrator and CLI wrapper for Claude Code and Codex sessions"
  homepage "https://github.com/victorarias/attn"
  url "${URL}"
  sha256 "${SHA256}"
  version "${VERSION}"
  license "GPL-3.0-only"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(output: bin/"attn"), "./cmd/attn"
  end

  test do
    assert_match "daemon offline", shell_output("#{bin}/attn status")
  end
end
EOF

echo "Updated Formula/attn.rb for ${VERSION_TAG}"
