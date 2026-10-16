# Builds Windows, Linux, and macOS binaries with size optimizations into ./dist.
param(
    [string]$SigningCertificateThumbprint,
    [ValidateSet('CurrentUser', 'LocalMachine')]
    [string]$SigningCertificateStore = 'CurrentUser',
    [string]$SignToolPath,
    [uri]$TimestampServer = 'http://timestamp.digicert.com'
)

$ErrorActionPreference = "Stop"

$repoRoot = $PSScriptRoot
Set-Location $repoRoot

$signingParameters = @{
    CertificateThumbprint = $SigningCertificateThumbprint
    CertificateStore = $SigningCertificateStore
    TimestampServer = $TimestampServer
}
if ($SignToolPath) {
    $signingParameters.SignToolPath = $SignToolPath
}
if ($SigningCertificateThumbprint) {
    & (Join-Path $repoRoot 'sign.ps1') @signingParameters -CheckOnly
}

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
        if ($LASTEXITCODE -ne 0) {
            throw "Windows icon generation failed for $($resource.Arch)."
        }
    }

    foreach ($target in $targets) {
        $env:GOOS = $target.OS
        $env:GOARCH = $target.Arch
        $env:CGO_ENABLED = "0"

        $name = "TwitchKickPointsMiner-$($target.OS)-$($target.Arch)$($target.Ext)"
        $outputPath = Join-Path $dist $name

        Write-Host "Building $name..."
        go build -trimpath -buildvcs=false -ldflags "-s -w" -o $outputPath .
        if ($LASTEXITCODE -ne 0) {
            throw "Build failed for $name."
        }
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

if ($SigningCertificateThumbprint) {
    & (Join-Path $repoRoot 'sign.ps1') @signingParameters
}
else {
    Write-Host 'Windows executables are unsigned. Supply -SigningCertificateThumbprint to sign a release.'
}

Write-Host "Builds complete -> $dist"
