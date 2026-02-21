class Attn < Formula
  desc "Desktop orchestrator and CLI wrapper for Claude Code and Codex sessions"
  homepage "https://github.com/victorarias/attn"
  url "https://github.com/victorarias/attn/archive/refs/tags/v0.2.11.tar.gz"
  sha256 "b51d4633a36db8b2f81d7749bf345b4780837730600c7f969f31718b385f1bd3"
  version "0.2.11"
  license "GPL-3.0-only"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(output: bin/"attn"), "./cmd/attn"
  end

  test do
    assert_match "daemon offline", shell_output("#{bin}/attn status")
  end
end
