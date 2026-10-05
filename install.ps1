[CmdletBinding()]
param(
    [string]$BinDir = $(if ($env:CTX_BIN_DIR) { $env:CTX_BIN_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\ctx\bin' }),
    [string]$ConfigDir = $(if ($env:CTX_HOME) { $env:CTX_HOME } else { Join-Path $env:APPDATA 'ctx' }),
    [string]$Version = 'latest',
    [string]$Adapters,
    [switch]$AllAdapters,
    [switch]$Minimal,
    [switch]$Interactive
)

$ErrorActionPreference = 'Stop'
$repositoryRoot = $PSScriptRoot
$localSource = Test-Path (Join-Path $repositoryRoot 'cmd\ctx\main.go')
$catalogAdapters = @('docker', 'podman', 'nerdctl', 'apple', 'rancher_desktop', 'orbstack', 'docker_desktop', 'firefox', 'zen', 'floorp', 'waterfox', 'librewolf', 'chrome', 'chromium', 'edge', 'brave', 'safari', 'vivaldi', 'opera', 'whale', 'arc', 'comet', 'dia', 'atlas', 'helium', 'kube', 'aws', 'gcloud', 'postgres', 'mysql', 'php', 'claude_code', 'codex', 'git', 'credman')
$needCatalog = $AllAdapters.IsPresent -or $Interactive.IsPresent -or [bool]$Adapters
$bundleRoot = $null
$downloadRoot = $null

$selectionModes = @($AllAdapters.IsPresent, $Minimal.IsPresent, $Interactive.IsPresent, [bool]$Adapters) | Where-Object { $_ }
if ($selectionModes.Count -gt 1) { throw 'Choose only one of -Adapters, -AllAdapters, -Minimal, or -Interactive.' }

New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
New-Item -ItemType Directory -Force -Path $ConfigDir | Out-Null

$ctxTarget = Join-Path $BinDir 'ctx.exe'
if (Test-Path $ctxTarget) {
    $owned = $false
    try {
        $installedVersion = @(& $ctxTarget version 2>$null) -join "`n"
        $owned = $LASTEXITCODE -eq 0 -and $installedVersion -match '^ctx '
    }
    catch { $owned = $false }
    if (-not $owned) { throw "$ctxTarget exists; choose another BinDir" }
}
if ($localSource) {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        throw 'Go is required when installing ctx from a source checkout.'
    }
    Push-Location $repositoryRoot
    try {
        & go build -o $ctxTarget ./cmd/ctx
        if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
    }
    finally {
        Pop-Location
    }
}
else {
    $architecture = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()) {
        'x64' { 'amd64' }
        'arm64' { 'arm64' }
        default { throw "Unsupported Windows architecture: $_" }
    }
    $release = if ($Version -eq 'latest') { 'latest/download' } else { "download/$Version" }
    $releaseBase = "https://github.com/webong/ctx/releases/$release"
    $asset = "ctx-windows-$architecture.zip"
    $downloadRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("ctx-install-" + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $downloadRoot | Out-Null
    $archive = Join-Path $downloadRoot $asset
    $checksums = Join-Path $downloadRoot 'checksums.txt'
    Invoke-WebRequest -UseBasicParsing -Uri "$releaseBase/$asset" -OutFile $archive
    Invoke-WebRequest -UseBasicParsing -Uri "$releaseBase/checksums.txt" -OutFile $checksums
    $escapedAsset = [regex]::Escape($asset)
    $checksumLine = Get-Content $checksums | Where-Object { $_ -match "\s\*?$escapedAsset`$" } | Select-Object -First 1
    if (-not $checksumLine) { throw "Release checksum is missing for $asset" }
    $expected = ($checksumLine -split '\s+')[0].ToLowerInvariant()
    $actual = (Get-FileHash -Algorithm SHA256 $archive).Hash.ToLowerInvariant()
    if ($actual -ne $expected) { throw "Release checksum verification failed for $asset" }
    Expand-Archive -Path $archive -DestinationPath $downloadRoot
    $bundleRoot = Join-Path $downloadRoot 'ctx'
    Copy-Item (Join-Path $bundleRoot 'bin\ctx.exe') $ctxTarget
    if ($needCatalog) {
        $catalogAsset = "ctx-adapters-windows-$architecture.zip"
        $catalogArchive = Join-Path $downloadRoot $catalogAsset
        Invoke-WebRequest -UseBasicParsing -Uri "$releaseBase/$catalogAsset" -OutFile $catalogArchive
        $escapedCatalogAsset = [regex]::Escape($catalogAsset)
        $catalogChecksumLine = Get-Content $checksums | Where-Object { $_ -match "\s\*?$escapedCatalogAsset`$" } | Select-Object -First 1
        if (-not $catalogChecksumLine) { throw "Release checksum is missing for $catalogAsset" }
        $catalogExpected = ($catalogChecksumLine -split '\s+')[0].ToLowerInvariant()
        $catalogActual = (Get-FileHash -Algorithm SHA256 $catalogArchive).Hash.ToLowerInvariant()
        if ($catalogActual -ne $catalogExpected) { throw "Release checksum verification failed for $catalogAsset" }
        Expand-Archive -Path $catalogArchive -DestinationPath $downloadRoot -Force
    }
}

$adaptersRoot = Join-Path $ConfigDir 'adapters'
$catalogRoot = Join-Path $ConfigDir 'catalog\adapters'
New-Item -ItemType Directory -Force -Path $adaptersRoot | Out-Null
if ($needCatalog) {
    New-Item -ItemType Directory -Force -Path $catalogRoot | Out-Null
    foreach ($adapter in $catalogAdapters) {
        $target = Join-Path $catalogRoot $adapter
        if (Test-Path $target) {
            Remove-Item -Recurse -Force $target
        }
        $adapterSource = if ($bundleRoot) { Join-Path $bundleRoot "adapters\$adapter" } else { Join-Path $repositoryRoot "adapters\$adapter" }
        Copy-Item -Recurse -Path $adapterSource -Destination $target
        if ($localSource -and $adapter -eq 'git') {
            $gitBinary = [System.IO.Path]::GetFullPath((Join-Path $target 'ctx-git.exe'))
            Push-Location $repositoryRoot
            try {
                & go build -o $gitBinary './adapters/git/native'
                if ($LASTEXITCODE -ne 0) { throw 'Failed to build Git adapter.' }
            }
            finally { Pop-Location }
            Remove-Item -Recurse -Force (Join-Path $target 'native')
        }
        if ($localSource -and $adapter -in @('firefox', 'zen', 'floorp', 'waterfox', 'librewolf', 'chrome', 'chromium', 'edge', 'brave', 'safari', 'vivaldi', 'opera', 'whale', 'arc', 'comet', 'dia', 'atlas', 'helium')) {
            $shareBinary = [System.IO.Path]::GetFullPath((Join-Path $target "ctx-$adapter-share.exe"))
            Push-Location $repositoryRoot
            try {
                & go build -o $shareBinary "./adapters/$adapter/native"
                if ($LASTEXITCODE -ne 0) { throw "Failed to build $adapter browser share adapter." }
            }
            finally { Pop-Location }
            Remove-Item -Recurse -Force (Join-Path $target 'native')
            if ($adapter -eq 'chromium') {
                Remove-Item -Recurse -Force (Join-Path $target 'engine')
            }
            if ($adapter -eq 'firefox') {
                Remove-Item -Recurse -Force (Join-Path $target 'engine')
            }
        }
        if ($localSource -and $adapter -eq 'credman') {
            $credentialBinary = [System.IO.Path]::GetFullPath((Join-Path $target 'ctx-credman.exe'))
            Push-Location $repositoryRoot
            try {
                & go build -o $credentialBinary './adapters/credman/native'
                if ($LASTEXITCODE -ne 0) { throw 'Failed to build Credential Manager adapter.' }
            }
            finally { Pop-Location }
            Remove-Item -Recurse -Force (Join-Path $target 'native')
            Remove-Item -Recurse -Force (Join-Path $target 'store')
        }
    }
}

$previousCtxHome = $env:CTX_HOME
$previousBinDir = $env:CTX_BIN_DIR
try {
    $env:CTX_HOME = $ConfigDir
    $env:CTX_BIN_DIR = $BinDir
    if ($needCatalog) {
        & $ctxTarget adapter refresh | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'Failed to refresh installed adapters.' }
    }
    if ($AllAdapters) { & $ctxTarget setup --all }
    elseif ($Minimal) { & $ctxTarget setup --minimal }
    elseif ($Adapters) { & $ctxTarget setup --adapters $Adapters }
    elseif ($Interactive) { & $ctxTarget setup }
    else { Write-Host 'No adapters selected. Rerun with -Interactive or -Adapters to obtain the optional catalog.' }
    if (($AllAdapters -or $Minimal -or $Adapters -or $Interactive) -and $LASTEXITCODE -ne 0) {
        throw 'Adapter setup failed.'
    }
}
finally {
    $env:CTX_HOME = $previousCtxHome
    $env:CTX_BIN_DIR = $previousBinDir
}

$configFile = Join-Path $ConfigDir 'config.toml'
if (-not (Test-Path $configFile)) {
    @'
# ctx configuration
# docker_default = "desktop-linux"
# podman_default = "podman-machine-default"
# nerdctl_default = "default"
'@ | Set-Content -Encoding UTF8 $configFile
}

$completionPath = Join-Path $ConfigDir 'ctx-completion.ps1'
& $ctxTarget completion powershell | Set-Content -Encoding UTF8 $completionPath
if ($LASTEXITCODE -ne 0) { throw 'Failed to generate PowerShell completion' }

if ($needCatalog) { Write-Host "Installed ctx.exe and the optional adapter catalog in $BinDir" }
else { Write-Host "Installed ctx.exe core in $BinDir (no adapter catalog downloaded)" }
Write-Host "Config: $configFile"
Write-Host "PowerShell completion: add `. '$completionPath' to your PowerShell profile"
if (($env:PATH -split ';') -notcontains $BinDir) {
    Write-Host "Add this directory before Docker, Podman, and nerdctl on PATH: $BinDir"
}
if ($downloadRoot) { Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $downloadRoot }
