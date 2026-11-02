[CmdletBinding(PositionalBinding = $false)]
param(
    [Parameter(Position = 0)][string]$Operation,
    [Parameter(Position = 1)][string]$Selection,
    [Parameter(Position = 2, ValueFromRemainingArguments = $true)][string[]]$Arguments
)

$container = $env:EXT_ADAPTER_REAL_COMMAND
if (-not $container -or -not (Test-Path -LiteralPath $container -PathType Leaf)) { [Console]::Error.WriteLine('apple: container CLI is not installed'); exit 127 }
if ($Arguments.Count -gt 0 -and $Arguments[0] -eq '--') { $Arguments = @($Arguments | Select-Object -Skip 1) }
$volumeImage = if ($env:EXT_VOLUME_IMAGE) { $env:EXT_VOLUME_IMAGE } else { 'alpine:3.21' }

function Invoke-Container([string[]]$CommandArguments) { & $container @CommandArguments; exit $LASTEXITCODE }
function Test-Available { & $container system version *> $null; return $LASTEXITCODE -eq 0 }
function Test-SafeImageLoad {
    $output = @(& $container --version 2>$null) -join "`n"
    if ($output -notmatch 'version\s+(\d+)\.(\d+)\.(\d+)') { return $false }
    $major = [int]$Matches[1]; $minor = [int]$Matches[2]; $patch = [int]$Matches[3]
    return $major -gt 1 -or ($major -eq 1 -and ($minor -gt 3 -or ($minor -eq 3 -and $patch -gt 0)))
}

switch ($Operation) {
    'list' { if (Test-Available) { 'local'; exit 0 }; exit 1 }
    'validate' { if ($Selection -ne 'local' -or -not (Test-Available)) { [Console]::Error.WriteLine('apple: local container runtime is unavailable'); exit 1 } }
    'doctor' { if ($Selection -and $Selection -ne 'local') { exit 1 }; if (-not (Test-Available)) { exit 1 } }
    'run' { if ($Selection -and $Selection -ne 'local') { exit 1 }; Invoke-Container $Arguments }
    'image_save' { Invoke-Container (@('image', 'save', '--output', $Arguments[0]) + @($Arguments | Select-Object -Skip 1)) }
    'image_load' { if (-not (Test-SafeImageLoad)) { [Console]::Error.WriteLine('apple: image import requires container newer than 1.3.0'); exit 1 }; Invoke-Container @('image', 'load', '--input', $Arguments[0]) }
    'volume_exists' { & $container volume inspect $Arguments[0] *> $null; exit $LASTEXITCODE }
    'volume_create' { Invoke-Container @('volume', 'create', $Arguments[0]) }
    'volume_export' { Invoke-Container @('run', '--rm', '-v', "$($Arguments[0]):/volume:ro", $volumeImage, 'tar', '-C', '/volume', '-cf', '-', '.') }
    'volume_import' { Invoke-Container @('run', '--rm', '-i', '-v', "$($Arguments[0]):/volume", $volumeImage, 'tar', '-C', '/volume', '-xf', '-') }
    default { exit 2 }
}
