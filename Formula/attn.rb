class Attn < Formula
  desc "Desktop orchestrator and CLI wrapper for Claude Code and Codex sessions"
  homepage "https://github.com/victorarias/attn"
  url "https://github.com/victorarias/attn/archive/refs/tags/v0.4.0.tar.gz"
  sha256 "5ea144d2c89a94bcbf1325754aadaf9776e489891cbc59705e525f46b45d6e80"
  version "0.4.0"
  license "GPL-3.0-only"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(output: bin/"attn", ldflags: "-X github.com/victorarias/attn/internal/buildinfo.Version=#{version}"), "./cmd/attn"
  end

  test do
    assert_equal version.to_s, shell_output("#{bin}/attn --version").strip
  end
end
