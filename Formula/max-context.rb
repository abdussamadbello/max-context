# Phase 7: Homebrew formula for max-context.
# To use: brew tap maxcontext/tap && brew install maxcontext/tap/max-context
# Or copy this file to your tap repo as Formula/max-context.rb.
class MaxContext < Formula
  desc "MCP server for codebase indexing and query (query_codebase, get_architecture)"
  homepage "https://github.com/maxcontext/max-context"
  version "0.1.0"
  license "MIT"

  on_macos do
    on_intel do
      url "https://github.com/maxcontext/max-context/releases/download/v0.1.0/max-context_0.1.0_darwin_amd64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
    on_arm do
      url "https://github.com/maxcontext/max-context/releases/download/v0.1.0/max-context_0.1.0_darwin_arm64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/maxcontext/max-context/releases/download/v0.1.0/max-context_0.1.0_linux_amd64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
    on_arm do
      url "https://github.com/maxcontext/max-context/releases/download/v0.1.0/max-context_0.1.0_linux_arm64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
  end

  def install
    bin.install "max-context"
  end

  test do
    assert_match "0.1.0", shell_output("#{bin}/max-context --version 2>&1").strip
  end
end
