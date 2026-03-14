class Attn < Formula
  desc "Desktop orchestrator and CLI wrapper for Claude Code and Codex sessions"
  homepage "https://github.com/victorarias/attn"
  url "https://github.com/victorarias/attn/archive/refs/tags/v0.3.0.tar.gz"
  sha256 "2fe757b990bcc3b13a171388163d6a9907a256ef85f2e934a474be78f25b7729"
  version "0.3.0"
  license "GPL-3.0-only"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(output: bin/"attn"), "./cmd/attn"
  end

  test do
    assert_match "daemon offline", shell_output("#{bin}/attn status")
  end
end
