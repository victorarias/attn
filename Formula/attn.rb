class Attn < Formula
  desc "Desktop orchestrator and CLI wrapper for Claude Code and Codex sessions"
  homepage "https://github.com/victorarias/attn"
  url "https://github.com/victorarias/attn/archive/refs/tags/v0.2.9.tar.gz"
  sha256 "9e15befec0568dda6b510fa28b80a901000e36ee74f7de815279cecfc42114c1"
  version "0.2.9"
  license "GPL-3.0-only"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(output: bin/"attn"), "./cmd/attn"
  end

  test do
    assert_match "daemon offline", shell_output("#{bin}/attn status")
  end
end
