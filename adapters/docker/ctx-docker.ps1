[CmdletBinding(PositionalBinding = $false)]
param(
    [Parameter(Position = 0)][string]$Operation,
    [Parameter(Position = 1)][string]$Selection,
    [Parameter(Position = 2, ValueFromRemainingArguments = $true)][string[]]$Arguments
)

$docker = $env:EXT_ADAPTER_REAL_COMMAND
if (-not $docker -or -not (Test-Path -LiteralPath $docker -PathType Leaf)) { [Console]::Error.WriteLine('docker: Docker CLI is not installed'); exit 127 }
if ($Arguments.Count -gt 0 -and $Arguments[0] -eq '--') { $Arguments = @($Arguments | Select-Object -Skip 1) }
$volumeImage = if ($env:EXT_VOLUME_IMAGE) { $env:EXT_VOLUME_IMAGE } else { 'alpine:3.21' }

function Invoke-Docker([string[]]$CommandArguments) { & $docker @CommandArguments; exit $LASTEXITCODE }
function Has-RunOverride {
    if ($env:DOCKER_CONTEXT -or $env:DOCKER_HOST -or ($Arguments.Count -gt 0 -and $Arguments[0] -eq 'context')) { return $true }
    foreach ($argument in $Arguments) { if ($argument -eq '--context' -or $argument -like '--context=*' -or $argument -eq '--host' -or $argument -like '--host=*' -or $argument -eq '-H' -or $argument -like '-H?*') { return $true } }
    return $false
}

switch ($Operation) {
    'list' { Invoke-Docker @('context', 'ls', '--format', '{{.Name}}') }
    'observe' {
        $names = @(& $docker context ls --format '{{.Name}}')
        if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
        $contexts = @($names | Where-Object { $_ } | ForEach-Object { @{ selection = [string]$_ } })
        @{ version = 1; contexts = $contexts } | ConvertTo-Json -Depth 4 -Compress
        exit 0
    }
    'validate' { & $docker context inspect $Selection *> $null; if ($LASTEXITCODE -ne 0) { [Console]::Error.WriteLine("docker: unknown context $Selection"); exit 1 } }
    'doctor' { if ($Selection) { & $docker context inspect $Selection *> $null } else { & $docker version *> $null }; exit $LASTEXITCODE }
    'run' { if (-not $Selection -or (Has-RunOverride)) { Invoke-Docker $Arguments } else { Invoke-Docker (@('--context', $Selection) + $Arguments) } }
    'build' {
        if ($Arguments.Count -lt 3 -or $Arguments[0] -ne '--cache-ref') { exit 2 }
        $cache = $Arguments[1]; $buildArguments = @($Arguments | Select-Object -Skip 2)
        if ($buildArguments.Count -gt 0 -and $buildArguments[0] -eq '--') { $buildArguments = @($buildArguments | Select-Object -Skip 1) }
        $prefix = if ($Selection) { @('--context', $Selection) } else { @() }
        Invoke-Docker ($prefix + @('buildx', 'build', '--cache-from', "type=registry,ref=$cache", '--cache-to', "type=registry,ref=$cache,mode=max") + $buildArguments)
    }
    'image_push' { Invoke-Docker (@('--context', $Selection, 'image', 'push') + $Arguments) }
    'image_pull' { Invoke-Docker (@('--context', $Selection, 'image', 'pull') + $Arguments) }
    'image_save' { Invoke-Docker (@('--context', $Selection, 'image', 'save', '-o', $Arguments[0]) + @($Arguments | Select-Object -Skip 1)) }
    'image_load' { Invoke-Docker @('--context', $Selection, 'image', 'load', '-i', $Arguments[0]) }
    'volume_exists' { & $docker --context $Selection volume inspect $Arguments[0] *> $null; exit $LASTEXITCODE }
    'volume_create' { Invoke-Docker @('--context', $Selection, 'volume', 'create', $Arguments[0]) }
    'volume_export' { Invoke-Docker @('--context', $Selection, 'run', '--rm', '-v', "$($Arguments[0]):/volume:ro", $volumeImage, 'tar', '-C', '/volume', '-cf', '-', '.') }
    'volume_import' { Invoke-Docker @('--context', $Selection, 'run', '--rm', '-i', '-v', "$($Arguments[0]):/volume", $volumeImage, 'tar', '-C', '/volume', '-xf', '-') }
    default { exit 2 }
}
