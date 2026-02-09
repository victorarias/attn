class Attn < Formula
  desc "Desktop orchestrator and CLI wrapper for Claude Code and Codex sessions"
  homepage "https://github.com/victorarias/attn"
  url "https://github.com/victorarias/attn/archive/refs/tags/v0.2.3.tar.gz"
  sha256 "bbd3fcdffa769d90359de8ecaefcd442858fb90ae4e967cc13e7914fd09b3a8f"
  version "0.2.3"
  license "GPL-3.0-only"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(output: bin/"attn"), "./cmd/attn"
  end

  test do
    assert_match "daemon offline", shell_output("#{bin}/attn status")
  end
end
