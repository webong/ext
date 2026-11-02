[CmdletBinding(PositionalBinding = $false)]
param(
    [Parameter(Position = 0)][string]$Operation,
    [Parameter(Position = 1)][string]$Selection,
    [Parameter(Position = 2, ValueFromRemainingArguments = $true)][string[]]$Arguments
)

if ($Arguments.Count -gt 0 -and $Arguments[0] -eq '--') { $Arguments = @($Arguments | Select-Object -Skip 1) }
$docker = $env:EXT_DOCKER_DESKTOP_CLI
if (-not $docker -and $env:ProgramFiles) {
    $bundled = Join-Path $env:ProgramFiles 'Docker\Docker\resources\bin\docker.exe'
    if (Test-Path -LiteralPath $bundled -PathType Leaf) { $docker = $bundled }
}
if (-not $docker -and $env:EXT_EXECUTABLE) {
    $docker = @(& $env:EXT_EXECUTABLE real docker 2>$null) | Select-Object -First 1
}
if (-not $docker -and -not $env:EXT_EXECUTABLE) {
    $found = Get-Command docker -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($found) { $docker = $found.Source }
}
function Test-DesktopCLI {
    if (-not $docker) { return $false }
    & $docker desktop version *> $null
    return $LASTEXITCODE -eq 0
}

switch ($Operation) {
    'list' { if (Test-DesktopCLI) { 'local' }; exit 0 }
    'validate' {
        if ($Selection -eq 'local' -and (Test-DesktopCLI)) { exit 0 }
        [Console]::Error.WriteLine('docker_desktop: Docker Desktop CLI is unavailable'); exit 1
    }
    'run' {
        if (-not (Test-DesktopCLI)) { [Console]::Error.WriteLine('docker_desktop: Docker Desktop CLI is unavailable'); exit 127 }
        if ($Arguments[0] -notin @('status', 'start', 'stop')) {
            [Console]::Error.WriteLine('docker_desktop: expected status, start, or stop'); exit 2
        }
        if ($Arguments[0] -eq 'status') {
            $status = @(& $docker desktop status 2>&1)
            if ($LASTEXITCODE -eq 0) { $status; exit 0 }
            if (($status -join ' ') -match 'Is Docker Desktop running\?') { 'Stopped'; exit 0 }
            $status | ForEach-Object { [Console]::Error.WriteLine($_) }
            exit 1
        }
        & $docker desktop $Arguments[0]
        exit $LASTEXITCODE
    }
    'doctor' {
        if (-not (Test-DesktopCLI)) { 'skip Docker Desktop CLI is not installed'; exit 0 }
        $status = @(& $docker desktop status 2>&1)
        if ($LASTEXITCODE -eq 0) { "ok   Docker Desktop status: $($status -join ' ')"; exit 0 }
        if (($status -join ' ') -match 'Is Docker Desktop running\?') { 'ok   Docker Desktop is stopped'; exit 0 }
        "fail Docker Desktop status: $($status -join ' ')"
        exit 1
    }
    default { exit 2 }
}
