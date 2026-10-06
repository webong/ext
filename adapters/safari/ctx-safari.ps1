[CmdletBinding(PositionalBinding = $false)]
param(
    [Parameter(Position = 0)][string]$Operation,
    [Parameter(Position = 1)][string]$Selection,
    [Parameter(Position = 2, ValueFromRemainingArguments = $true)][string[]]$Arguments
)

switch ($Operation) {
    'share' {
        if ($Arguments.Count -gt 0 -and $Arguments[0] -eq '--') { $Arguments = @($Arguments | Select-Object -Skip 1) }
        $helper = Join-Path $PSScriptRoot 'ctx-safari-share.exe'
        & $helper 'share' $Selection '--' @Arguments
        exit $LASTEXITCODE
    }
    'list' { exit 0 }
    'validate' { exit 1 }
    'doctor' { exit 1 }
    'open' { [Console]::Error.WriteLine('safari: Safari contexts are only available on macOS'); exit 1 }
    default { exit 2 }
}
