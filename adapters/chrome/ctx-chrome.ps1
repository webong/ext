[CmdletBinding(PositionalBinding = $false)]
param(
    [Parameter(Position = 0)][string]$Operation,
    [Parameter(Position = 1)][string]$Selection,
    [Parameter(Position = 2, ValueFromRemainingArguments = $true)][string[]]$Arguments
)

function Find-Chrome {
    $command = Get-Command chrome -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($command) { return $command.Source }
    $candidates = @()
    if ($env:LOCALAPPDATA) { $candidates += (Join-Path $env:LOCALAPPDATA 'Google\Chrome\Application\chrome.exe') }
    if ($env:ProgramFiles) { $candidates += (Join-Path $env:ProgramFiles 'Google\Chrome\Application\chrome.exe') }
    if (${env:ProgramFiles(x86)}) { $candidates += (Join-Path ${env:ProgramFiles(x86)} 'Google\Chrome\Application\chrome.exe') }
    foreach ($candidate in $candidates) { if (Test-Path -LiteralPath $candidate -PathType Leaf) { return $candidate } }
    return $null
}
function Get-ProfilesRoot { return (Join-Path $env:LOCALAPPDATA 'Google\Chrome\User Data') }
function Test-Profile([string]$Name) { return (Test-Path -LiteralPath (Join-Path (Join-Path (Get-ProfilesRoot) $Name) 'Preferences') -PathType Leaf) }
function Get-Profiles {
    $root = Get-ProfilesRoot
    if (-not (Test-Path -LiteralPath $root -PathType Container)) { return }
    Get-ChildItem -LiteralPath $root -Directory -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -eq 'Default' -or $_.Name -like 'Profile *' } |
        Where-Object { Test-Path -LiteralPath (Join-Path $_.FullName 'Preferences') -PathType Leaf } |
        Sort-Object Name | ForEach-Object { "chrome:$($_.Name)" }
}

$chrome = Find-Chrome
switch ($Operation) {
    'share' {
        if ($Arguments.Count -gt 0 -and $Arguments[0] -eq '--') { $Arguments = @($Arguments | Select-Object -Skip 1) }
        $helper = Join-Path $PSScriptRoot 'ctx-chrome-share.exe'
        & $helper 'share' $Selection '--' @Arguments
        exit $LASTEXITCODE
    }
    'list' { if ($chrome) { Get-Profiles }; exit 0 }
    'validate' { if (-not $chrome -or -not (Test-Profile $Selection)) { exit 1 } }
    'doctor' { if (-not $chrome -or ($Selection -and -not (Test-Profile $Selection))) { exit 1 } }
    'open' {
        if (-not $chrome -or -not (Test-Profile $Selection)) { [Console]::Error.WriteLine("chrome: profile $Selection is unavailable"); exit 1 }
        if ($Arguments.Count -gt 0 -and $Arguments[0] -eq '--') { $Arguments = @($Arguments | Select-Object -Skip 1) }
        & $chrome "--profile-directory=$Selection" @Arguments
        exit $LASTEXITCODE
    }
    default { exit 2 }
}
