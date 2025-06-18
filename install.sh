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
