class Specstory < Formula
  desc "A claude code wrapper that saves your conversation history to markdown"
  homepage "https://github.com/specstoryai/getspecstory"
  version "1.0.0"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/specstoryai/getspecstory/releases/download/v1.0.0/SpecStoryCLI_Darwin_arm64.tar.gz"
      sha256 "2771d291e36dc9f60de83089a2f68d92e8691dbf6042cf3836bbbed6ea771fc7"
    else
      url "https://github.com/specstoryai/getspecstory/releases/download/v1.0.0/SpecStoryCLI_Darwin_x86_64.tar.gz"
      sha256 "17aa867f644525aa1cae8d7ec526691a09cba3e879712463de85b230d8059d3a"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/specstoryai/getspecstory/releases/download/v1.0.0/SpecStoryCLI_Linux_arm64.tar.gz"
      sha256 "1068b2a0caa24d70f663a41e92de92bd8c462773a90645c327437892bba2243e"
    else
      url "https://github.com/specstoryai/getspecstory/releases/download/v1.0.0/SpecStoryCLI_Linux_x86_64.tar.gz"
      sha256 "6fb289dd663ad3cd521f96f534602a7134e71d230d31faa8f9b957a75569a2c3"
    end
  end

  def install
    bin.install "specstory"
  end

  test do
    assert_match "specstory", shell_output("#{bin}/specstory --version")
  end
end
