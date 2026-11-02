[CmdletBinding(PositionalBinding = $false)]
param(
    [Parameter(Position = 0)][string]$Operation,
    [Parameter(Position = 1)][string]$Selection,
    [Parameter(Position = 2, ValueFromRemainingArguments = $true)][string[]]$Arguments
)

$nerdctl = $env:EXT_ADAPTER_REAL_COMMAND
if (-not $nerdctl -or -not (Test-Path -LiteralPath $nerdctl -PathType Leaf)) { [Console]::Error.WriteLine('nerdctl: nerdctl is not installed'); exit 127 }
if ($Arguments.Count -gt 0 -and $Arguments[0] -eq '--') { $Arguments = @($Arguments | Select-Object -Skip 1) }
$volumeImage = if ($env:EXT_VOLUME_IMAGE) { $env:EXT_VOLUME_IMAGE } else { 'alpine:3.21' }

function Get-AddressPrefix { if ($env:EXT_MANAGER_ADDRESS) { return @('--address', $env:EXT_MANAGER_ADDRESS) }; return @() }
function Invoke-Nerdctl([string[]]$CommandArguments) { $prefix = @(Get-AddressPrefix); & $nerdctl @prefix @CommandArguments; exit $LASTEXITCODE }
function Get-Namespaces { $prefix = @(Get-AddressPrefix); @(& $nerdctl @prefix namespace ls --quiet) }
function Has-RunOverride {
    if ($env:CONTAINERD_NAMESPACE -or $env:CONTAINERD_ADDRESS -or ($Arguments.Count -gt 0 -and $Arguments[0] -eq 'namespace')) { return $true }
    foreach ($argument in $Arguments) { if ($argument -eq '--namespace' -or $argument -like '--namespace=*' -or $argument -eq '--address' -or $argument -like '--address=*' -or $argument -eq '-n' -or $argument -like '-n?*' -or $argument -eq '-a' -or $argument -like '-a?*') { return $true } }
    return $false
}

switch ($Operation) {
    'list' { & $nerdctl namespace ls --quiet; exit $LASTEXITCODE }
    'validate' { if ((Get-Namespaces) -notcontains $Selection) { [Console]::Error.WriteLine("nerdctl: unknown namespace $Selection"); exit 1 } }
    'doctor' { if ($Selection) { if ((Get-Namespaces) -notcontains $Selection) { exit 1 } } else { & $nerdctl version *> $null; exit $LASTEXITCODE } }
    'run' { if (-not $Selection -or (Has-RunOverride)) { Invoke-Nerdctl $Arguments } else { Invoke-Nerdctl (@('--namespace', $Selection) + $Arguments) } }
    'build' {
        if ($Arguments.Count -lt 3 -or $Arguments[0] -ne '--cache-ref') { exit 2 }
        $cache = $Arguments[1]; $buildArguments = @($Arguments | Select-Object -Skip 2)
        if ($buildArguments.Count -gt 0 -and $buildArguments[0] -eq '--') { $buildArguments = @($buildArguments | Select-Object -Skip 1) }
        $prefix = if ($Selection) { @('--namespace', $Selection) } else { @() }
        Invoke-Nerdctl ($prefix + @('build', '--cache-from', "type=registry,ref=$cache", '--cache-to', "type=registry,ref=$cache,mode=max") + $buildArguments)
    }
    'image_push' { Invoke-Nerdctl (@('--namespace', $Selection, 'push') + $Arguments) }
    'image_pull' { Invoke-Nerdctl (@('--namespace', $Selection, 'pull') + $Arguments) }
    'image_save' { Invoke-Nerdctl (@('--namespace', $Selection, 'save', '-o', $Arguments[0]) + @($Arguments | Select-Object -Skip 1)) }
    'image_load' { Invoke-Nerdctl @('--namespace', $Selection, 'load', '-i', $Arguments[0]) }
    'volume_exists' { $prefix = @(Get-AddressPrefix); & $nerdctl @prefix --namespace $Selection volume inspect $Arguments[0] *> $null; exit $LASTEXITCODE }
    'volume_create' { Invoke-Nerdctl @('--namespace', $Selection, 'volume', 'create', $Arguments[0]) }
    'volume_export' { Invoke-Nerdctl @('--namespace', $Selection, 'run', '--rm', '-v', "$($Arguments[0]):/volume:ro", $volumeImage, 'tar', '-C', '/volume', '-cf', '-', '.') }
    'volume_import' { Invoke-Nerdctl @('--namespace', $Selection, 'run', '--rm', '-i', '-v', "$($Arguments[0]):/volume", $volumeImage, 'tar', '-C', '/volume', '-xf', '-') }
    default { exit 2 }
}
