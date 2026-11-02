param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
$ErrorActionPreference = 'Stop'

function Get-RealNode {
    if ($env:CTX_ADAPTER_REAL_COMMAND -and (Test-Path -LiteralPath $env:CTX_ADAPTER_REAL_COMMAND -PathType Leaf)) {
        return $env:CTX_ADAPTER_REAL_COMMAND
    }
    foreach ($name in @('node')) {
        if ($env:CTX_EXECUTABLE -and (Test-Path -LiteralPath $env:CTX_EXECUTABLE -PathType Leaf)) {
            $resolved = & $env:CTX_EXECUTABLE real $name 2>$null
            if ($LASTEXITCODE -eq 0 -and $resolved -and (Test-Path -LiteralPath $resolved -PathType Leaf)) { return $resolved }
        }
        $command = Get-Command $name -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($command) { return $command.Source }
    }
    return $null
}

# CTX passes every copy it found in CTX_PROGRAMS, one tab-separated line each:
# name, path, realpath, version, installers, on_path. Outside CTX only the copy
# on PATH is known.
function Get-NodeCandidates {
    if ($null -ne $env:CTX_PROGRAMS) {
        $rows = foreach ($line in ($env:CTX_PROGRAMS -split "`n")) {
            $f = $line.TrimEnd("`r") -split "`t"
            if ($f.Count -ge 6 -and @('node') -contains $f[0] -and $f[1]) {
                [pscustomobject]@{ rank = $(if ($f[5] -eq '1') { 0 } else { 1 }); executable = $f[1]; manager = $(if ($f[4]) { $f[4] } else { 'path' }) }
            }
        }
        return @($rows | Sort-Object rank)
    }
    $executable = Get-RealNode
    if ($executable) { return @([pscustomobject]@{ rank = 0; executable = $executable; manager = 'path' }) }
    return @()
}

function Get-NodeStates {
    $seen = @{}
    foreach ($candidate in Get-NodeCandidates) {
        if (-not (Test-Path -LiteralPath $candidate.executable -PathType Leaf)) { continue }
        $versionArguments = @('-p', 'process.versions.node')
        $version = (& $candidate.executable @versionArguments 2>$null | Out-String).Trim()
        if ($LASTEXITCODE -ne 0 -or -not $version) { continue }
        $selection = $version.Split('.')[0]
        if ($seen.ContainsKey($selection)) { continue }
        $seen[$selection] = $true
        [pscustomobject]@{ selection = $selection; executable = $candidate.executable; version = $version; manager = $candidate.manager }
    }
}

function Find-NodeState($selection) {
    Get-NodeStates | Where-Object { $_.selection -eq $selection } | Select-Object -First 1
}

$operation = if ($Arguments.Count) { $Arguments[0] } else { '' }
$payload = @($Arguments | Select-Object -Skip 1)
switch ($operation) {
    'list' {
        Get-NodeStates | ForEach-Object { $_.selection }
        exit 0
    }
    'observe' {
        $contexts = @(Get-NodeStates | ForEach-Object { @{ selection = $_.selection; attributes = @{ kind = 'node'; version = $_.version; isolation = 'host'; manager = $_.manager; executable = $_.executable } } })
        @{ version = 1; contexts = $contexts } | ConvertTo-Json -Compress -Depth 5
        exit 0
    }
    'validate' {
        $selection = if ($payload.Count) { $payload[0] } else { '' }
        if (-not $selection -or -not (Find-NodeState $selection)) { [Console]::Error.WriteLine("node: Node.js $selection is not installed"); exit 1 }
        exit 0
    }
    'doctor' {
        $selection = if ($payload.Count) { $payload[0] } else { '' }
        $state = if ($selection) { Find-NodeState $selection } else { $null }
        if ($selection -and -not $state) { [Console]::Error.WriteLine("node: Node.js $selection is not installed"); exit 1 }
        $executable = if ($state) { $state.executable } else { Get-RealNode }
        if (-not $executable) { [Console]::Error.WriteLine('node: Node.js is not installed'); exit 127 }
        & $executable --version
        exit $LASTEXITCODE
    }
    'run' {
        $selection = ''
        if ($payload.Count -and $payload[0] -ne '--') { $selection = $payload[0]; $payload = @($payload | Select-Object -Skip 1) }
        if ($payload.Count -and $payload[0] -eq '--') { $payload = @($payload | Select-Object -Skip 1) }
        $state = if ($selection) { Find-NodeState $selection } else { $null }
        if ($selection -and -not $state) { [Console]::Error.WriteLine("node: Node.js $selection is not installed"); exit 1 }
        $executable = if ($state) { $state.executable } else { Get-RealNode }
        if (-not $executable) { [Console]::Error.WriteLine('node: Node.js is not installed'); exit 127 }
        & $executable @payload
        exit $LASTEXITCODE
    }
    default { exit 2 }
}
