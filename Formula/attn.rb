class Attn < Formula
  desc "Desktop orchestrator and CLI wrapper for Claude Code and Codex sessions"
  homepage "https://github.com/victorarias/attn"
  url "https://github.com/victorarias/attn/archive/refs/tags/v0.2.0.tar.gz"
  sha256 "a92ffbb03bda4b9ce510b5154575d0d9e3cbe604069cbf876d72cecd296fd9af"
  version "0.2.0"
  license "GPL-3.0-only"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(output: bin/"attn"), "./cmd/attn"
  end

  test do
    assert_match "daemon offline", shell_output("#{bin}/attn status")
  end
end
