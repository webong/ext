[CmdletBinding(PositionalBinding = $false)]
param(
    [Parameter(Position = 0)][string]$Operation,
    [Parameter(Position = 1)][string]$Selection,
    [Parameter(Position = 2, ValueFromRemainingArguments = $true)][string[]]$Arguments
)

$psql = Get-Command psql -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $psql) { [Console]::Error.WriteLine('postgres: psql is not installed'); exit 127 }

switch ($Operation) {
    'configure' {
        if ($Arguments.Count -gt 0 -and $Arguments[0] -eq '--') { $Arguments = @($Arguments | Select-Object -Skip 1) }
        $serviceFile = ''
        for ($index = 0; $index -lt $Arguments.Count; $index++) {
            if ($Arguments[$index] -eq '--service-file') {
                if ($index + 1 -ge $Arguments.Count) { exit 2 }
                $index++; $serviceFile = $Arguments[$index]
            } elseif ($Arguments[$index] -like '--service-file=*') {
                $serviceFile = $Arguments[$index].Substring(15)
            } else {
                [Console]::Error.WriteLine("postgres: unsupported selection option $($Arguments[$index])"); exit 2
            }
        }
        "postgres_service`t$Selection"
        if ($serviceFile) { "postgres_service_file`t$serviceFile" }
    }
    { $_ -eq 'validate' -or $_ -eq 'doctor' } { exit 0 }
    'run' {
        if ($Arguments.Count -gt 0 -and $Arguments[0] -eq '--') { $Arguments = @($Arguments | Select-Object -Skip 1) }
        $commandName = if ($env:EXT_ADAPTER_COMMAND) { $env:EXT_ADAPTER_COMMAND } else { 'psql' }
        if ($commandName -eq 'postgres') { $commandName = 'psql' }
        $client = Get-Command $commandName -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if (-not $client) { [Console]::Error.WriteLine("postgres: $commandName is not installed"); exit 127 }
        if (-not $env:PGSERVICE) { $env:PGSERVICE = $Selection }
        if (-not $env:PGSERVICEFILE -and $env:EXT_ADAPTER_VALUE_POSTGRES_SERVICE_FILE) {
            $env:PGSERVICEFILE = $env:EXT_ADAPTER_VALUE_POSTGRES_SERVICE_FILE
        }
        & $client.Source @Arguments
        exit $LASTEXITCODE
    }
    default { exit 2 }
}
