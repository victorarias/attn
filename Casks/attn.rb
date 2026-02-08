cask "attn" do
  version :latest
  sha256 :no_check

  url "https://github.com/victorarias/attn/releases/latest/download/attn_aarch64.dmg"
  name "attn"
  desc "Desktop orchestrator for Claude Code and Codex sessions"
  homepage "https://github.com/victorarias/attn"

  depends_on arch: :arm64

  app "attn.app"

  caveats <<~EOS
    Optional CLI/daemon wrapper:
      brew install victorarias/attn/attn
  EOS
end
