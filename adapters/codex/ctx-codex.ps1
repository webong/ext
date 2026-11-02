[CmdletBinding(PositionalBinding = $false)]
param(
    [Parameter(Position = 0)][string]$Operation,
    [Parameter(Position = 1, ValueFromRemainingArguments = $true)][string[]]$Arguments
)

$codex = $env:EXT_ADAPTER_REAL_COMMAND
if ($Arguments.Count -gt 0 -and $Arguments[0] -eq '--') { $Arguments = @($Arguments | Select-Object -Skip 1) }
function Invoke-Codex([string[]]$CommandArguments) {
    & $codex @CommandArguments
    exit $LASTEXITCODE
}

switch ($Operation) {
    'validate' { if (-not $codex) { [Console]::Error.WriteLine('codex: Codex CLI is not installed'); exit 127 } }
    'doctor' {
        if (-not $codex) { [Console]::Error.WriteLine('codex: Codex CLI is not installed'); exit 127 }
        Invoke-Codex @('--version')
    }
    'run' {
        if (-not $codex) { [Console]::Error.WriteLine('codex: Codex CLI is not installed'); exit 127 }
        Invoke-Codex $Arguments
    }
    'hook' {
        if ($Arguments.Count -eq 0) { [Console]::Error.WriteLine('codex: hook needs an event name'); exit 2 }
        $handler = if ($env:EXT_ADAPTER_VALUE_COMPUTER_HOOK_COMMAND) { $env:EXT_ADAPTER_VALUE_COMPUTER_HOOK_COMMAND } else { $env:EXT_COMPUTER_HOOK_COMMAND }
        if (-not $handler) { [Console]::Error.WriteLine('codex: configure computer_hook_command or EXT_COMPUTER_HOOK_COMMAND'); exit 127 }
        & $handler $Arguments[0]
        exit $LASTEXITCODE
    }
    'plugin' {
        if (-not $codex) { [Console]::Error.WriteLine('codex: Codex CLI is not installed'); exit 127 }
        Invoke-Codex (@('plugin') + $Arguments)
    }
    default { exit 2 }
}
