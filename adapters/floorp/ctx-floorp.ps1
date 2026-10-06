[CmdletBinding(PositionalBinding = $false)]
param(
    [Parameter(Position = 0)][string]$Operation,
    [Parameter(Position = 1)][string]$Selection,
    [Parameter(Position = 2, ValueFromRemainingArguments = $true)][string[]]$Arguments
)
function Get-ProfilesIni {
    if (-not $env:APPDATA) { return $null }
    return (Join-Path $env:APPDATA 'Floorp\profiles.ini')
}
function Get-Profiles {
    $ini = Get-ProfilesIni
    if (-not $ini -or -not (Test-Path -LiteralPath $ini -PathType Leaf)) { return }
    $active = $false
    foreach ($line in Get-Content -LiteralPath $ini) {
        if ($line -match '^\[Profile[0-9]+\]') { $active = $true; continue }
        if ($line -match '^\[') { $active = $false; continue }
        if ($active -and $line -match '^Name=(.*)$') { $Matches[1].Trim() }
    }
}
function Test-Profile([string]$Name) {
    if ([System.IO.Path]::IsPathRooted($Name)) {
        return ((Test-Path -LiteralPath (Join-Path $Name 'cookies.sqlite') -PathType Leaf) -or (Test-Path -LiteralPath $Name -PathType Leaf))
    }
    return (@(Get-Profiles) -contains $Name)
}
switch ($Operation) {
    'share' {
        if ($Arguments.Count -gt 0 -and $Arguments[0] -eq '--') { $Arguments = @($Arguments | Select-Object -Skip 1) }
        & (Join-Path $PSScriptRoot 'ctx-floorp-share.exe') 'share' $Selection '--' @Arguments
        exit $LASTEXITCODE
    }
    'list' { Get-Profiles | ForEach-Object { "floorp:$_" }; exit 0 }
    'validate' { if (-not (Test-Profile $Selection)) { exit 1 } }
    'doctor' { if ($Selection -and -not (Test-Profile $Selection)) { exit 1 } }
    default { exit 2 }
}
