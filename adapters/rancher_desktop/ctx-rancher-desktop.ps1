[CmdletBinding(PositionalBinding = $false)]
param(
    [Parameter(Position = 0)][string]$Operation,
    [Parameter(Position = 1)][string]$Selection,
    [Parameter(Position = 2, ValueFromRemainingArguments = $true)][string[]]$Arguments
)

if ($Arguments.Count -gt 0 -and $Arguments[0] -eq '--') { $Arguments = @($Arguments | Select-Object -Skip 1) }
$rdctl = $env:EXT_RANCHER_RDCTL
if (-not $rdctl) {
    $found = Get-Command rdctl -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($found) { $rdctl = $found.Source }
}

switch ($Operation) {
    'list' { if ($rdctl) { 'local' }; exit 0 }
    'validate' {
        if ($Selection -eq 'local' -and $rdctl) { exit 0 }
        [Console]::Error.WriteLine('rancher_desktop: Rancher Desktop is unavailable'); exit 1
    }
    'run' {
        if (-not $rdctl) { [Console]::Error.WriteLine('rancher_desktop: rdctl is unavailable'); exit 127 }
        switch ($Arguments[0]) {
            'status' { & $rdctl info; exit $LASTEXITCODE }
            'start' { & $rdctl start; exit $LASTEXITCODE }
            'stop' { & $rdctl shutdown; exit $LASTEXITCODE }
            default { [Console]::Error.WriteLine('rancher_desktop: expected status, start, or stop'); exit 2 }
        }
    }
    'doctor' {
        if (-not $rdctl) { 'skip Rancher Desktop is not installed'; exit 0 }
        $info = @(& $rdctl info 2>&1)
        $infoCode = $LASTEXITCODE
        if ($infoCode -eq 0) { 'ok   Rancher Desktop is running' }
        elseif (($info -join ' ') -match 'state "?(Stopped|ShutDown)"?') {
            'ok   Rancher Desktop is stopped'
            $infoCode = 0
        }
        else { "fail Rancher Desktop is not running: $($info | Select-Object -First 1)" }
        if ($infoCode -eq 0) {
            $settings = @(& $rdctl list-settings 2>$null) -join "`n"
            if ($LASTEXITCODE -eq 0) {
                try {
                    $engine = ($settings | ConvertFrom-Json).containerEngine.name
                    if ($engine -eq 'containerd') { 'ok   containerd engine; Docker Buildx/Compose links are not used by nerdctl' }
                    elseif ($engine -in @('moby', 'docker')) { 'info Moby engine; Docker CLI plugin ownership is managed by the selected Docker config' }
                }
                catch { 'info Rancher engine settings could not be parsed' }
            }
        }
        exit $(if ($infoCode -eq 0) { 0 } else { 1 })
    }
    default { exit 2 }
}
