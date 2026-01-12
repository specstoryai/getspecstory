# SpecStory CLI Distribution Guide

This guide sets up automated releases from a **private source repo** to a **public release repo** with both Homebrew tap and curl installation for the `specstory` CLI.

## Repository Setup
- **Private repo**: The source code (current location)
- **Public repo**: `specstoryai/getspecstory` (release artifacts only)  
- **Public tap repo**: `specstoryai/homebrew-tap` (Homebrew formulas)

## 1. Prepare Your Go Project

### Add version handling to `main.go`:
```go
package main

import (
    "flag"
    "fmt"
    "os"
)

var version = "dev"

func main() {
    var showVersion = flag.Bool("version", false, "Show version")
    flag.Parse()

    if *showVersion {
        fmt.Printf("specstory %s\n", version)
        os.Exit(0)
    }

    // Your CLI logic here
    fmt.Println("Welcome to GetSpecStory!")
}
```

### Create `.goreleaser.yml` in your **private repo**:
```yaml
project_name: getspecstory

builds:
  - env:
      - CGO_ENABLED=0
    goos:
      - linux
      - darwin
    goarch:
      - amd64
      - arm64
    binary: specstory
    ldflags:
      - -s -w -X main.version={{.Version}}

archives:
  - format: tar.gz
    name_template: >-
      {{ .ProjectName }}_
      {{- title .Os }}_
      {{- if eq .Arch "amd64" }}x86_64
      {{- else }}{{ .Arch }}{{ end }}
  - format: zip
    name_template: >-
      {{ .ProjectName }}_
      {{- title .Os }}_
      {{- if eq .Arch "amd64" }}x86_64
      {{- else }}{{ .Arch }}{{ end }}

release:
  github:
    owner: specstoryai
    name: getspecstory  # This pushes to the PUBLIC repo
```

## 2. Set Up Cross-Repo Release Automation

### Create Personal Access Token:
1. Go to GitHub, Profile → Settings → Developer settings (very bottom of page) → Personal access tokens → Tokens (classic)
2. Click to "Generate new token" with `repo` permissions
3. Name it something like `specstory-cli-release-token`
4. Set the expiration to custom, and 1 year
5. Click "Generate token"
6. Copy the token

### Add token to private repo:
1. In your private repo: Settings → Secrets and variables → Actions
2. Add new secret: `PUBLIC_REPO_TOKEN` = your token

### Create `.github/workflows/release.yml` in your **private repo**:
```yaml
name: Release to Public Repo

on:
  push:
    tags:
      - 'v*'

permissions:
  contents: read

jobs:
  goreleaser:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v4
        with:
          go-version: '1.21'
      - uses: goreleaser/goreleaser-action@v5
        with:
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.PUBLIC_REPO_TOKEN }}
```

## 3. Create Homebrew Tap

### Create new **PUBLIC** repository: `specstoryai/homebrew-tap`
> This must be public for Homebrew to access the formulas

### Create `Formula/specstory.rb`:
```ruby
class Specstory < Formula
  desc "A claude code wrapper that saves your conversation history to markdown"
  homepage "https://github.com/specstoryai/getspecstory"
  version "1.0.0"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/specstoryai/getspecstory/releases/download/v1.0.0/SpecStoryCLI_Darwin_arm64.tar.gz"
      sha256 "ARM64_SHA256_HERE"
    else
      url "https://github.com/specstoryai/getspecstory/releases/download/v1.0.0/SpecStoryCLI_Darwin_x86_64.tar.gz"
      sha256 "AMD64_SHA256_HERE"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/specstoryai/getspecstory/releases/download/v1.0.0/SpecStoryCLI_Linux_arm64.tar.gz"
      sha256 "LINUX_ARM64_SHA256_HERE"
    else
      url "https://github.com/specstoryai/getspecstory/releases/download/v1.0.0/SpecStoryCLI_Linux_x86_64.tar.gz"
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
```

## 4. Create Curl Installer

### Create `install.sh` in **public repo** (`specstoryai/getspecstory`):
```bash
#!/bin/bash
set -e

REPO="specstoryai/getspecstory"
BINARY_NAME="specstory"
INSTALL_DIR="/usr/local/bin"

# Detect platform
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case $ARCH in
    x86_64) ARCH="x86_64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *) echo "❌ Unsupported architecture: $ARCH"; exit 1 ;;
esac

case $OS in
    darwin) OS="Darwin" ;;
    linux) OS="Linux" ;;
    *) echo "❌ Unsupported OS: $OS"; exit 1 ;;
esac

echo "🚀 Installing SpecStory CLI for $OS $ARCH..."

# Get latest version
VERSION=$(curl -s "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

if [ -z "$VERSION" ]; then
    echo "❌ Failed to get latest version"
    exit 1
fi

# Download and install
FILENAME="getspecstory_${OS}_${ARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/$REPO/releases/download/$VERSION/$FILENAME"
TMP_DIR=$(mktemp -d)

echo "📥 Downloading $VERSION..."
curl -sL "$DOWNLOAD_URL" | tar -xz -C "$TMP_DIR"

# Install with sudo if needed
if [ -w "$INSTALL_DIR" ]; then
    mv "$TMP_DIR/$BINARY_NAME" "$INSTALL_DIR/"
else
    echo "🔐 Installing to $INSTALL_DIR (requires sudo)..."
    sudo mv "$TMP_DIR/$BINARY_NAME" "$INSTALL_DIR/"
fi

chmod +x "$INSTALL_DIR/$BINARY_NAME"
rm -rf "$TMP_DIR"

echo "✅ SpecStory installed successfully!"
echo "Try: $BINARY_NAME --version"
```

## 5. Release Process

### Create your first release in **private repo**:
```bash
# In private repo, commit all files
git add .
git commit -m "Setup distribution"
git push origin your-branch

# Create release tag in private repo
git tag -a v1.0.0 -m "Release v1.0.0"
git push origin v1.0.0
```

### The GitHub Action will:
1. Build binaries from private repo
2. Create release in public repo (`specstoryai/getspecstory`)
3. Upload artifacts to public repo

### After release completes, get SHA256 hashes:
```bash
curl -sL https://github.com/specstoryai/getspecstory/releases/download/v0.1.0/SpecStoryCLI_Darwin_arm64.tar.gz | shasum -a 256
curl -sL https://github.com/specstoryai/getspecstory/releases/download/v0.1.0/SpecStoryCLI_Darwin_x86_64.tar.gz | shasum -a 256
curl -sL https://github.com/specstoryai/getspecstory/releases/download/v0.1.0/SpecStoryCLI_Linux_arm64.tar.gz | shasum -a 256
curl -sL https://github.com/specstoryai/getspecstory/releases/download/v0.1.0/SpecStoryCLI_Linux_x86_64.tar.gz | shasum -a 256
```

### Update Homebrew formula with real SHA256 values and push to tap repo.

## 6. Update README.md

### Add to **public repo** (`specstoryai/getspecstory`) README.md:

```markdown
# GetSpecStory

## Installation

### Homebrew (Recommended)

```zsh
brew tap specstoryai/homebrew-tap
brew install specstory
```

### Curl Script

```zsh
curl -sSL https://raw.githubusercontent.com/specstoryai/getspecstory/main/install.sh | bash
```

### Manual Download

Download from [GitHub Releases](https://github.com/specstoryai/getspecstory/releases/latest)

## Usage

```zsh
getspecstory --help
getspecstory --version
```
```

## 7. Future Releases

For new versions, create tags in your **private repo**:

```zsh
# In private repo
git tag -a v1.1.0 -m "Release v1.1.0"
git push origin v1.1.0
```

The GitHub Action will automatically build and release to the public repo. Then update the version, the Homebrew formula with the new version and the SHA256 hashes in the [homebrew-tap](https://github.com/specstoryai/homebrew-tap/blob/main/Formula/specstory.rb) formula.

## File Structure Summary

Other files omitted for brevity from private-repo

```
private-repo/                         # Your private source repo
├── main.go                           # With version handling
├── .goreleaser.yml                   # Release config (points to public repo)
└── .github/workflows/release.yml     # Builds & releases to public repo

specstoryai/getspecstory/             # Public release repo
├── install.sh                        # Curl installer
├── README.md                         # With install instructions
└── releases/                         # Auto-generated by GitHub Action

specstoryai/homebrew-tap/             # Public tap repo
├── Formula/specstory.rb              # Homebrew formula
└── README.md
```

**Workflow**: Create tags in private repo → GitHub Action builds → Releases to public repo → Users install `specstory` from public repo