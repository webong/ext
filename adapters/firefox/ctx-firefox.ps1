[CmdletBinding(PositionalBinding = $false)]
param(
    [Parameter(Position = 0)][string]$Operation,
    [Parameter(Position = 1)][string]$Selection,
    [Parameter(Position = 2, ValueFromRemainingArguments = $true)][string[]]$Arguments
)

function Find-Firefox {
    $command = Get-Command firefox -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($command) { return $command.Source }
    $candidates = @()
    if ($env:ProgramFiles) { $candidates += (Join-Path $env:ProgramFiles 'Mozilla Firefox\firefox.exe') }
    if (${env:ProgramFiles(x86)}) { $candidates += (Join-Path ${env:ProgramFiles(x86)} 'Mozilla Firefox\firefox.exe') }
    if ($env:LOCALAPPDATA) { $candidates += (Join-Path $env:LOCALAPPDATA 'Mozilla Firefox\firefox.exe') }
    foreach ($candidate in $candidates) { if (Test-Path -LiteralPath $candidate -PathType Leaf) { return $candidate } }
    return $null
}
function Get-ProfilesFile { return (Join-Path $env:APPDATA 'Mozilla\Firefox\profiles.ini') }
function Get-Profiles {
    $file = Get-ProfilesFile
    if (-not (Test-Path -LiteralPath $file -PathType Leaf)) { return @() }
    $profiles = @(); $inProfile = $false
    foreach ($line in Get-Content -LiteralPath $file) {
        if ($line -match '^\[Profile\d+\]$') { $inProfile = $true; continue }
        if ($line -match '^\[') { $inProfile = $false; continue }
        if ($inProfile -and $line -match '^Name=(.+)$') { $profiles += $Matches[1] }
    }
    return $profiles
}
function Test-Profile([string]$Name) { return (Get-Profiles) -contains $Name }

$firefox = Find-Firefox
switch ($Operation) {
    'share' {
        if ($Arguments.Count -gt 0 -and $Arguments[0] -eq '--') { $Arguments = @($Arguments | Select-Object -Skip 1) }
        $helper = Join-Path $PSScriptRoot 'ctx-firefox-share.exe'
        & $helper 'share' $Selection '--' @Arguments
        exit $LASTEXITCODE
    }
    'list' { if ($firefox) { Get-Profiles | ForEach-Object { "firefox:$_" } }; exit 0 }
    'validate' { if (-not $firefox -or -not (Test-Profile $Selection)) { exit 1 } }
    'doctor' { if (-not $firefox -or ($Selection -and -not (Test-Profile $Selection))) { exit 1 } }
    'open' {
        if (-not $firefox -or -not (Test-Profile $Selection)) { [Console]::Error.WriteLine("firefox: profile $Selection is unavailable"); exit 1 }
        if ($Arguments.Count -gt 0 -and $Arguments[0] -eq '--') { $Arguments = @($Arguments | Select-Object -Skip 1) }
        & $firefox -P $Selection @Arguments
        exit $LASTEXITCODE
    }
    default { exit 2 }
}
