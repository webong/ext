param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
$ErrorActionPreference = 'Stop'

function Get-RealPhp {
    if ($env:EXT_ADAPTER_REAL_COMMAND -and (Test-Path -LiteralPath $env:EXT_ADAPTER_REAL_COMMAND -PathType Leaf)) {
        return $env:EXT_ADAPTER_REAL_COMMAND
    }
    if ($env:EXT_EXECUTABLE -and (Test-Path -LiteralPath $env:EXT_EXECUTABLE -PathType Leaf)) {
        $resolved = & $env:EXT_EXECUTABLE real php 2>$null
        if ($LASTEXITCODE -eq 0 -and $resolved -and (Test-Path -LiteralPath $resolved -PathType Leaf)) { return $resolved }
    }
    $command = Get-Command php -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($command) { return $command.Source }
    return $null
}

function Get-PhpState {
    $executable = Get-RealPhp
    if (-not $executable) { return $null }
    $version = (& $executable -r 'echo PHP_VERSION;' 2>$null | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or -not $version) { return $null }
    $parts = $version.Split('.')
    [pscustomobject]@{ selection = "$($parts[0]).$($parts[1])"; executable = $executable; version = $version; manager = 'path' }
}

$operation = if ($Arguments.Count) { $Arguments[0] } else { '' }
$payload = @($Arguments | Select-Object -Skip 1)
$state = Get-PhpState
switch ($operation) {
    'list' {
        if ($state) { $state.selection }
        exit 0
    }
    'observe' {
        $contexts = @()
        if ($state) { $contexts = @(@{ selection = $state.selection; attributes = @{ version = $state.version; manager = $state.manager } }) }
        @{ version = 1; contexts = $contexts } | ConvertTo-Json -Compress -Depth 5
        exit 0
    }
    'validate' {
        $selection = if ($payload.Count) { $payload[0] } else { '' }
        if (-not $state -or $selection -ne $state.selection) { [Console]::Error.WriteLine("php: PHP $selection is not installed"); exit 1 }
        exit 0
    }
    'doctor' {
        $selection = if ($payload.Count) { $payload[0] } else { '' }
        if ($selection -and (-not $state -or $selection -ne $state.selection)) { [Console]::Error.WriteLine("php: PHP $selection is not installed"); exit 1 }
        $executable = if ($state) { $state.executable } else { Get-RealPhp }
        if (-not $executable) { [Console]::Error.WriteLine('php: PHP CLI is not installed'); exit 127 }
        & $executable -v
        exit $LASTEXITCODE
    }
    'run' {
        $selection = ''
        if ($payload.Count -and $payload[0] -ne '--') { $selection = $payload[0]; $payload = @($payload | Select-Object -Skip 1) }
        if ($payload.Count -and $payload[0] -eq '--') { $payload = @($payload | Select-Object -Skip 1) }
        if ($selection -and (-not $state -or $selection -ne $state.selection)) { [Console]::Error.WriteLine("php: PHP $selection is not installed"); exit 1 }
        $executable = if ($state) { $state.executable } else { Get-RealPhp }
        if (-not $executable) { [Console]::Error.WriteLine('php: PHP CLI is not installed'); exit 127 }
        & $executable @payload
        exit $LASTEXITCODE
    }
    default { exit 2 }
}
