class Attn < Formula
  desc "Desktop orchestrator and CLI wrapper for Claude Code and Codex sessions"
  homepage "https://github.com/victorarias/attn"
  url "https://github.com/victorarias/attn/archive/refs/tags/v0.5.0.tar.gz"
  sha256 "450cfa15ee1ed869d9cdac3e0ccd1ba8403f14a8b587a8c9afefc7b18b3fb9dc"
  version "0.5.0"
  license "GPL-3.0-only"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(output: bin/"attn", ldflags: "-X github.com/victorarias/attn/internal/buildinfo.Version=#{version}"), "./cmd/attn"
  end

  test do
    assert_equal version.to_s, shell_output("#{bin}/attn --version").strip
  end
end
