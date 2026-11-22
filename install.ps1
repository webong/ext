[CmdletBinding()]
param(
    [string]$BinDir = $(if ($env:EXT_BIN_DIR) { $env:EXT_BIN_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\ext\bin' }),
    [string]$ConfigDir = $(if ($env:EXT_HOME) { $env:EXT_HOME } else { Join-Path $env:APPDATA 'ext' }),
    [string]$Version = $env:EXT_VERSION,
    [switch]$Ext,
    [switch]$AllAdapters,
    [switch]$Minimal,
    [string]$Adapters,
    [switch]$Interactive
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$temporary = Join-Path ([System.IO.Path]::GetTempPath()) ('ext-install-' + [guid]::NewGuid().ToString('N'))
$bundle = Join-Path $temporary 'ext'
try {
    New-Item -ItemType Directory -Force -Path $temporary | Out-Null
    if (Test-Path (Join-Path $root 'cmd\ext\main.go')) {
        if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'Go is required for source installation.' }
        $architecture = (& go env GOARCH).Trim()
        $packages = Join-Path $bundle 'bin\ext-packages'
        New-Item -ItemType Directory -Force -Path $packages | Out-Null
        Push-Location $root
        try {
            $env:CGO_ENABLED = '0'
            & go build -trimpath -ldflags '-s -w' -o (Join-Path $bundle 'bin\ext.exe') ./cmd/ext
            if ($LASTEXITCODE -ne 0) { throw 'Building ext failed.' }
            foreach ($product in @('ctx', 'ctn', 'cto')) {
                $packageDir = Join-Path $packages "ext%2f$product"
                New-Item -ItemType Directory -Force -Path $packageDir | Out-Null
                $guest = Join-Path $packageDir "$product.exe"
                & go build -trimpath -ldflags '-s -w' -o $guest "./src/$product/plugin"
                if ($LASTEXITCODE -ne 0) { throw "Building the $product package failed." }
                & go run ./cmd/ext/tools/package-product $product dev windows $architecture $guest
                if ($LASTEXITCODE -ne 0) { throw "Packaging $product failed." }
            }
        } finally { Pop-Location }
    } else {
        $releases = Invoke-RestMethod 'https://api.github.com/repos/webong/ext/releases?per_page=100'
        if (-not $Version) { $Version = ($releases | Where-Object { $_.tag_name -like 'ext-v*' } | Select-Object -First 1).tag_name }
        if (-not $Version) { throw 'No ext release was found.' }
        $Version = $Version -replace '^ext-v', '' -replace '^v', ''
        $architecture = if ([Environment]::Is64BitOperatingSystem -and $env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
        $asset = "ext-windows-$architecture.zip"
        $base = "https://github.com/webong/ext/releases/download/ext-v$Version"
        $archive = Join-Path $temporary $asset
        Invoke-WebRequest "$base/$asset" -OutFile $archive
        $checksums = Join-Path $temporary 'checksums.txt'
        Invoke-WebRequest "$base/checksums.txt" -OutFile $checksums
        $line = Get-Content $checksums | Where-Object { $_ -match "\s$([regex]::Escape($asset))$" } | Select-Object -First 1
        if (-not $line) { throw "Checksum missing for $asset." }
        $expected = ($line -split '\s+')[0].ToLowerInvariant()
        $actual = (Get-FileHash -Algorithm SHA256 $archive).Hash.ToLowerInvariant()
        if ($actual -ne $expected) { throw "Checksum mismatch for $asset." }
        Expand-Archive -Path $archive -DestinationPath $temporary
    }

    $binary = Join-Path $bundle 'bin\ext.exe'
    $packages = Join-Path $bundle 'bin\ext-packages'
    if (-not (Test-Path $binary) -or -not (Test-Path $packages)) { throw 'The ext bundle is incomplete.' }
    New-Item -ItemType Directory -Force -Path $BinDir, $ConfigDir | Out-Null
    $target = Join-Path $BinDir 'ext.exe'
    if (Test-Path $target) {
        $installedVersion = @(& $target version 2>$null) -join "`n"
        if ($LASTEXITCODE -ne 0 -or $installedVersion -notmatch '^ext ') { throw "$target is not an ext executable." }
    }
    Copy-Item $binary $target -Force
    $targetPackages = Join-Path $BinDir 'ext-packages'
    if (Test-Path $targetPackages) { Remove-Item $targetPackages -Recurse -Force }
    Copy-Item $packages $targetPackages -Recurse
    $env:EXT_HOME = $ConfigDir
    & $target commands | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Installed product packages failed verification.' }

    if ($AllAdapters -or $Minimal -or $Adapters -or $Interactive) {
        if (Test-Path (Join-Path $root 'adapters')) {
            $catalog = Join-Path $ConfigDir 'catalog\adapters'
            New-Item -ItemType Directory -Force -Path $catalog | Out-Null
            Get-ChildItem (Join-Path $root 'adapters') -Directory | ForEach-Object { Copy-Item $_.FullName (Join-Path $catalog $_.Name) -Recurse -Force }
        }
        $env:EXT_BIN_DIR = $BinDir
        if ($AllAdapters) { & $target ctx setup --all }
        elseif ($Minimal) { & $target ctx setup --minimal }
        elseif ($Adapters) { & $target ctx setup --adapters $Adapters }
        else { & $target ctx setup }
        if ($LASTEXITCODE -ne 0) { throw 'CTX adapter setup failed.' }
    }
    & $target ctx completion powershell | Set-Content -Encoding UTF8 (Join-Path $ConfigDir 'ctx-completion.ps1')
    if ($LASTEXITCODE -ne 0) { throw 'CTX completion generation failed.' }
    Write-Host "Installed ext and its CTX, CTN and CTO packages in $BinDir"
} finally {
    if (Test-Path $temporary) { Remove-Item $temporary -Recurse -Force }
}
