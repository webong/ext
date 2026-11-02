[CmdletBinding(PositionalBinding = $false)]
param(
    [Parameter(Position = 0)][string]$Operation,
    [Parameter(Position = 1)][string]$Selection,
    [Parameter(Position = 2, ValueFromRemainingArguments = $true)][string[]]$Arguments
)

$mysql = Get-Command mysql -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $mysql) { [Console]::Error.WriteLine('mysql: mysql client is not installed'); exit 127 }

switch ($Operation) {
    { $_ -eq 'validate' -or $_ -eq 'doctor' } { exit 0 }
    'run' {
        if ($Arguments.Count -gt 0 -and $Arguments[0] -eq '--') { $Arguments = @($Arguments | Select-Object -Skip 1) }
        $commandName = if ($env:EXT_ADAPTER_COMMAND) { $env:EXT_ADAPTER_COMMAND } else { 'mysql' }
        $client = Get-Command $commandName -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if (-not $client) { [Console]::Error.WriteLine("mysql: $commandName is not installed"); exit 127 }
        $hasLoginPath = $false
        foreach ($argument in $Arguments) { if ($argument -eq '--login-path' -or $argument -like '--login-path=*') { $hasLoginPath = $true } }
        if ($hasLoginPath) { & $client.Source @Arguments } else { & $client.Source "--login-path=$Selection" @Arguments }
        exit $LASTEXITCODE
    }
    default { exit 2 }
}
