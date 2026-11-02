[CmdletBinding(PositionalBinding = $false)]
param(
    [Parameter(Position = 0)][string]$Operation,
    [Parameter(Position = 1)][string]$Selection,
    [Parameter(Position = 2, ValueFromRemainingArguments = $true)][string[]]$Arguments
)

$podman = $env:EXT_ADAPTER_REAL_COMMAND
if (-not $podman -or -not (Test-Path -LiteralPath $podman -PathType Leaf)) { [Console]::Error.WriteLine('podman: Podman CLI is not installed'); exit 127 }
if ($Arguments.Count -gt 0 -and $Arguments[0] -eq '--') { $Arguments = @($Arguments | Select-Object -Skip 1) }
$volumeImage = if ($env:EXT_VOLUME_IMAGE) { $env:EXT_VOLUME_IMAGE } else { 'alpine:3.21' }

function Invoke-Podman([string[]]$CommandArguments) { & $podman @CommandArguments; exit $LASTEXITCODE }
function Get-Connections { @(& $podman system connection list --format '{{.Name}}') }
function Has-RunOverride {
    if ($env:CONTAINER_CONNECTION -or $env:CONTAINER_HOST -or ($Arguments.Count -gt 0 -and $Arguments[0] -eq 'machine')) { return $true }
    if ($Arguments.Count -gt 1 -and $Arguments[0] -eq 'system' -and $Arguments[1] -eq 'connection') { return $true }
    foreach ($argument in $Arguments) { if ($argument -eq '--connection' -or $argument -like '--connection=*' -or $argument -eq '--url' -or $argument -like '--url=*' -or $argument -eq '--remote' -or $argument -eq '-c' -or $argument -like '-c?*' -or $argument -eq '-r' -or $argument -like '-r?*') { return $true } }
    return $false
}

switch ($Operation) {
    'list' { & $podman system connection list --format '{{.Name}}'; exit $LASTEXITCODE }
    'validate' { if ((Get-Connections) -notcontains $Selection) { [Console]::Error.WriteLine("podman: unknown connection $Selection"); exit 1 } }
    'doctor' { if ($Selection) { if ((Get-Connections) -notcontains $Selection) { exit 1 } } else { & $podman version *> $null; exit $LASTEXITCODE } }
    'run' { if (-not $Selection -or (Has-RunOverride)) { Invoke-Podman $Arguments } else { Invoke-Podman (@('--connection', $Selection) + $Arguments) } }
    'build' {
        if ($Arguments.Count -lt 3 -or $Arguments[0] -ne '--cache-ref') { exit 2 }
        $cache = $Arguments[1]; $buildArguments = @($Arguments | Select-Object -Skip 2)
        if ($buildArguments.Count -gt 0 -and $buildArguments[0] -eq '--') { $buildArguments = @($buildArguments | Select-Object -Skip 1) }
        $prefix = if ($Selection) { @('--connection', $Selection) } else { @() }
        Invoke-Podman ($prefix + @('build', '--layers', '--cache-from', $cache, '--cache-to', $cache) + $buildArguments)
    }
    'image_push' { Invoke-Podman (@('--connection', $Selection, 'image', 'push') + $Arguments) }
    'image_pull' { Invoke-Podman (@('--connection', $Selection, 'image', 'pull') + $Arguments) }
    'image_save' { Invoke-Podman (@('--connection', $Selection, 'image', 'save', '-o', $Arguments[0]) + @($Arguments | Select-Object -Skip 1)) }
    'image_load' { Invoke-Podman @('--connection', $Selection, 'image', 'load', '-i', $Arguments[0]) }
    'volume_exists' { & $podman --connection $Selection volume inspect $Arguments[0] *> $null; exit $LASTEXITCODE }
    'volume_create' { Invoke-Podman @('--connection', $Selection, 'volume', 'create', $Arguments[0]) }
    'volume_export' { Invoke-Podman @('--connection', $Selection, 'run', '--rm', '-v', "$($Arguments[0]):/volume:ro", $volumeImage, 'tar', '-C', '/volume', '-cf', '-', '.') }
    'volume_import' { Invoke-Podman @('--connection', $Selection, 'run', '--rm', '-i', '-v', "$($Arguments[0]):/volume", $volumeImage, 'tar', '-C', '/volume', '-xf', '-') }
    default { exit 2 }
}
