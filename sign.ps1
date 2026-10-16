# Signs Windows executables with an existing code signing certificate.
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$CertificateThumbprint,

    [string[]]$Path = @(),

    [ValidateSet('CurrentUser', 'LocalMachine')]
    [string]$CertificateStore = 'CurrentUser',

    [string]$SignToolPath,

    [uri]$TimestampServer = 'http://timestamp.digicert.com',

    [switch]$CheckOnly
)

$ErrorActionPreference = 'Stop'

if ($env:OS -ne 'Windows_NT') {
    throw 'Authenticode signing requires Windows.'
}

$thumbprint = ($CertificateThumbprint -replace '\s', '').ToUpperInvariant()
if ($thumbprint -notmatch '^[0-9A-F]{40}$') {
    throw 'CertificateThumbprint must contain the 40 hexadecimal characters identifying your code signing certificate.'
}
if (-not $TimestampServer.IsAbsoluteUri -or $TimestampServer.Scheme -notin @('http', 'https')) {
    throw 'TimestampServer must be an absolute HTTP or HTTPS RFC 3161 timestamp endpoint.'
}

$certificatePath = "Cert:\$CertificateStore\My\$thumbprint"
$certificate = Get-Item -LiteralPath $certificatePath -ErrorAction SilentlyContinue
if ($null -eq $certificate) {
    throw "No code signing certificate found at $certificatePath. A publicly trusted certificate and its private key are required."
}
if (-not $certificate.HasPrivateKey) {
    throw 'The selected certificate has no accessible private key. Connect its hardware token or configure its signing provider.'
}
if ($certificate.NotBefore -gt (Get-Date) -or $certificate.NotAfter -le (Get-Date)) {
    throw 'The selected certificate is outside its validity period.'
}
if ($certificate.EnhancedKeyUsageList.ObjectId -notcontains '1.3.6.1.5.5.7.3.3') {
    throw 'The selected certificate does not permit code signing.'
}
if ($certificate.Subject -eq $certificate.Issuer) {
    throw 'A self signed certificate does not meet the public trust requirement for Smart App Control.'
}

$chain = New-Object System.Security.Cryptography.X509Certificates.X509Chain
try {
    $usage = New-Object System.Security.Cryptography.Oid('1.3.6.1.5.5.7.3.3')
    [void]$chain.ChainPolicy.ApplicationPolicy.Add($usage)
    if (-not $chain.Build($certificate)) {
        $reason = ($chain.ChainStatus | ForEach-Object { $_.StatusInformation.Trim() }) -join '; '
        throw "The signing certificate chain is not valid: $reason"
    }
}
finally {
    $chain.Dispose()
}

if (-not $SignToolPath) {
    $installedTool = Get-Command signtool.exe -ErrorAction SilentlyContinue
    if ($installedTool) {
        $SignToolPath = $installedTool.Source
    }
    else {
        $architecture = switch ($env:PROCESSOR_ARCHITECTURE) {
            'ARM64' { 'arm64' }
            'AMD64' { 'x64' }
            default { 'x86' }
        }
        $sdkRoot = Join-Path ${env:ProgramFiles(x86)} 'Windows Kits\10\bin'
        if (Test-Path -LiteralPath $sdkRoot) {
            $versions = Get-ChildItem -LiteralPath $sdkRoot -Directory |
                Where-Object { $_.Name -match '^\d+\.\d+\.\d+\.\d+$' } |
                Sort-Object { [version]$_.Name } -Descending
            foreach ($version in $versions) {
                $candidate = Join-Path $version.FullName "$architecture\signtool.exe"
                if (Test-Path -LiteralPath $candidate -PathType Leaf) {
                    $SignToolPath = $candidate
                    break
                }
            }
        }
    }
}
if (-not $SignToolPath -or -not (Test-Path -LiteralPath $SignToolPath -PathType Leaf)) {
    throw 'SignTool was not found. Install the Windows SDK signing tools or supply SignToolPath.'
}
$SignToolPath = (Resolve-Path -LiteralPath $SignToolPath).Path

if ($CheckOnly) {
    Write-Host "Signing certificate and SignTool are ready: $($certificate.Subject)"
    return
}
if ($Path.Count -eq 0) {
    $Path = @(
        (Join-Path $PSScriptRoot 'dist\TwitchKickPointsMiner-windows-amd64.exe'),
        (Join-Path $PSScriptRoot 'dist\TwitchKickPointsMiner-windows-arm64.exe')
    )
}
$targets = foreach ($target in $Path) {
    $item = Get-Item -LiteralPath $target
    if ($item.PSIsContainer -or $item.Extension -ine '.exe') {
        throw "Signing target must be an executable file: $target"
    }
    $item.FullName
}

foreach ($target in $targets) {
    $signArguments = @(
        'sign', '/sha1', $thumbprint, '/s', 'My', '/fd', 'SHA256',
        '/tr', $TimestampServer.AbsoluteUri, '/td', 'SHA256',
        '/d', 'Twitch Kick Points Miner',
        '/du', 'https://github.com/atalaydenknalbant/twitch-kick-points-miner'
    )
    if ($CertificateStore -eq 'LocalMachine') {
        $signArguments += '/sm'
    }
    $signArguments += $target
    & $SignToolPath @signArguments
    if ($LASTEXITCODE -ne 0) {
        throw "Signing or timestamping failed for $target (exit $LASTEXITCODE)."
    }
    & $SignToolPath verify /pa /all /tw $target
    if ($LASTEXITCODE -ne 0) {
        throw "Signature verification failed for $target (exit $LASTEXITCODE)."
    }
    $signature = Get-AuthenticodeSignature -LiteralPath $target
    if ($signature.Status -ne 'Valid' -or $null -eq $signature.TimeStamperCertificate -or $signature.SignerCertificate.Thumbprint -ne $thumbprint) {
        throw "The executable does not have the expected valid, timestamped signature: $target"
    }
    Write-Host "Signed and verified: $target"
}
