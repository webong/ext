[CmdletBinding(PositionalBinding = $false)]
param(
    [Parameter(Position = 0)][string]$Operation,
    [Parameter(Position = 1)][string]$Selection,
    [Parameter(Position = 2, ValueFromRemainingArguments = $true)][string[]]$Arguments
)
function Get-ProfilesRoot {
    if ('Naver\Naver Whale\User Data' -eq '') { return $null }
    $base = if ($false) { $env:APPDATA } else { $env:LOCALAPPDATA }
    if (-not $base) { return $null }
    return (Join-Path $base 'Naver\Naver Whale\User Data')
}
function Test-Profile([string]$Name) {
    if ([System.IO.Path]::IsPathRooted($Name)) {
        return ((Test-Path -LiteralPath (Join-Path $Name 'Preferences') -PathType Leaf) -or (Test-Path -LiteralPath $Name -PathType Leaf))
    }
    $root = Get-ProfilesRoot
    if (-not $root) { return $false }
    $directory = if ($Name -eq 'root') { $root } else { Join-Path $root $Name }
    return (Test-Path -LiteralPath (Join-Path $directory 'Preferences') -PathType Leaf)
}
function Get-Profiles {
    $root = Get-ProfilesRoot
    if (-not $root -or -not (Test-Path -LiteralPath $root -PathType Container)) { return }
    if (Test-Path -LiteralPath (Join-Path $root 'Preferences') -PathType Leaf) { 'whale:root' }
    Get-ChildItem -LiteralPath $root -Directory -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -eq 'Default' -or $_.Name -like 'Profile *' } |
        Where-Object { Test-Path -LiteralPath (Join-Path $_.FullName 'Preferences') -PathType Leaf } |
        Sort-Object Name | ForEach-Object { "whale:$($_.Name)" }
}
switch ($Operation) {
    'share' {
        if ($Arguments.Count -gt 0 -and $Arguments[0] -eq '--') { $Arguments = @($Arguments | Select-Object -Skip 1) }
        & (Join-Path $PSScriptRoot 'ctx-whale-share.exe') 'share' $Selection '--' @Arguments
        exit $LASTEXITCODE
    }
    'list' { Get-Profiles; exit 0 }
    'validate' { if (-not (Test-Profile $Selection)) { exit 1 } }
    'doctor' { if ($Selection -and -not (Test-Profile $Selection)) { exit 1 } }
    default { exit 2 }
}
