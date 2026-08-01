#!/bin/zsh
# Builds the current tree and installs it to /Applications/SpecStory.app,
# replacing any previous copy and refreshing the LaunchServices registration
# so Finder shows the current icon.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [[ ! -x Resources/bin/specstory_darwin_arm64 ]]; then
  ./scripts/vendor-cli.sh
fi
xcodegen generate
xcodebuild -project SpecStory.xcodeproj -scheme SpecStory -configuration Debug \
  -derivedDataPath build/DerivedData build | grep -E "BUILD (SUCCEEDED|FAILED)"

BUILT="$ROOT/build/DerivedData/Build/Products/Debug/SpecStory.app"
DEST="/Applications/SpecStory.app"

killall SpecStory 2>/dev/null || true
rm -rf "$DEST"
cp -R "$BUILT" "$DEST"

# Nudge Finder and LaunchServices to drop cached metadata (icons especially).
touch "$DEST"
/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -f "$DEST"

echo "Installed $DEST"
echo "Launch with: open /Applications/SpecStory.app"
