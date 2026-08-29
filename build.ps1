# Builds Windows, Linux, and macOS binaries with size optimizations into ./dist.
$ErrorActionPreference = "Stop"

$repoRoot = $PSScriptRoot
Set-Location $repoRoot

$dist = Join-Path $repoRoot "dist"
New-Item -ItemType Directory -Force -Path $dist | Out-Null

$iconPath = Join-Path $repoRoot "assets/app.ico"
$windowsResources = @(
    @{ Arch = "amd64"; Path = Join-Path $repoRoot "build_icon_windows_amd64.syso" },
    @{ Arch = "arm64"; Path = Join-Path $repoRoot "build_icon_windows_arm64.syso" }
)

if (!(Test-Path $iconPath)) {
    throw "Windows icon not found at $iconPath"
}

Write-Host "Generating Windows icon resource..."

$targets = @(
    @{ OS = "windows"; Arch = "amd64"; Ext = ".exe" },
    @{ OS = "windows"; Arch = "arm64"; Ext = ".exe" },
    @{ OS = "linux";   Arch = "amd64"; Ext = "" },
    @{ OS = "linux";   Arch = "arm64"; Ext = "" },
    @{ OS = "darwin";  Arch = "amd64"; Ext = "" },
    @{ OS = "darwin";  Arch = "arm64"; Ext = "" }
)

$originalEnv = @{
    GOOS        = $env:GOOS
    GOARCH      = $env:GOARCH
    CGO_ENABLED = $env:CGO_ENABLED
}

try {
    foreach ($resource in $windowsResources) {
        go run github.com/akavel/rsrc@v0.10.2 -arch $resource.Arch -ico $iconPath -o $resource.Path
    }

    foreach ($target in $targets) {
        $env:GOOS = $target.OS
        $env:GOARCH = $target.Arch
        $env:CGO_ENABLED = "0"

        $name = "TwitchKickPointsMiner-$($target.OS)-$($target.Arch)$($target.Ext)"
        $outputPath = Join-Path $dist $name

        Write-Host "Building $name..."
        go build -trimpath -buildvcs=false -ldflags "-s -w" -o $outputPath .
    }
}
finally {
    foreach ($resource in $windowsResources) {
        if (Test-Path $resource.Path) {
            Remove-Item -Path $resource.Path -Force
        }
    }

    $env:GOOS = $originalEnv.GOOS
    $env:GOARCH = $originalEnv.GOARCH
    $env:CGO_ENABLED = $originalEnv.CGO_ENABLED
}

Write-Host "Builds complete -> $dist"
