# Build specstory-cli for linux, darwin (amd64 and arm64), and windows (amd64). Output to specstory-monorepo/bin.
# Run from anywhere.

param(
    [string]$OutputRelativePath = "dist"
)

$ErrorActionPreference = "Stop"

# Strip leading backslash/slash to avoid double separator when joining
$OutputRelativePath = $OutputRelativePath.TrimStart('\').TrimStart('/')

$StartDir  = (Get-Location).Path
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$CliDir    = (Resolve-Path (Join-Path $ScriptDir "..")).Path
$DestDir   = Join-Path $StartDir $OutputRelativePath

Set-Location $CliDir
New-Item -ItemType Directory -Force -Path $DestDir | Out-Null
Get-ChildItem -Path $DestDir -Filter "specstory_*" | Remove-Item -Force

$version = $env:VERSION
if (-not $version) {
    $version = git describe --tags --always --dirty 2>$null
    if (-not $version) { $version = "dev" }
}

$posthogKey = if ($env:POSTHOG_API_KEY) { $env:POSTHOG_API_KEY } else { "" }
$ldflags = "-s -w -X main.version=$version -X github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics.apiKey=$posthogKey"

# os goarch filename_arch file_ext
$targets = @(
    @{ Os = "linux";   Arch = "amd64"; FilenameArch = "x86_64"; Ext = "" },
    @{ Os = "linux";   Arch = "arm64"; FilenameArch = "arm64";  Ext = "" },
    @{ Os = "darwin";  Arch = "amd64"; FilenameArch = "x86_64"; Ext = "" },
    @{ Os = "darwin";  Arch = "arm64"; FilenameArch = "arm64";  Ext = "" },
    @{ Os = "windows"; Arch = "amd64"; FilenameArch = "x86_64"; Ext = ".exe" },
    @{ Os = "windows"; Arch = "arm64"; FilenameArch = "arm64";  Ext = ".exe" }
)

foreach ($t in $targets) {
    $out = Join-Path $DestDir "specstory_$($t.Os)_$($t.FilenameArch)$($t.Ext)"
    Write-Host "Building $out..."
    $env:CGO_ENABLED = "0"
    $env:GOOS        = $t.Os
    $env:GOARCH      = $t.Arch
    go build -ldflags=$ldflags -o $out ./main.go
}

# Restore env vars so they don't leak into the caller's session
Remove-Item Env:\CGO_ENABLED -ErrorAction SilentlyContinue
Remove-Item Env:\GOOS        -ErrorAction SilentlyContinue
Remove-Item Env:\GOARCH      -ErrorAction SilentlyContinue

Write-Host "Done. Binaries in $DestDir"
