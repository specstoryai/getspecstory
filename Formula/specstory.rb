class Specstory < Formula
  desc "A claude code wrapper that saves your conversation history to markdown"
  homepage "https://github.com/specstoryai/getspecstory"
  version "1.0.0"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/specstoryai/getspecstory/releases/download/v1.0.0/getspecstory_Darwin_arm64.tar.gz"
      sha256 "ARM64_SHA256_HERE"
    else
      url "https://github.com/specstoryai/getspecstory/releases/download/v1.0.0/getspecstory_Darwin_x86_64.tar.gz"
      sha256 "AMD64_SHA256_HERE"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/specstoryai/getspecstory/releases/download/v1.0.0/getspecstory_Linux_arm64.tar.gz"
      sha256 "LINUX_ARM64_SHA256_HERE"
    else
      url "https://github.com/specstoryai/getspecstory/releases/download/v1.0.0/getspecstory_Linux_x86_64.tar.gz"
      sha256 "LINUX_AMD64_SHA256_HERE"
    end
  end

  def install
    bin.install "specstory"
  end

  test do
    assert_match "specstory", shell_output("#{bin}/specstory --version")
  end
end
