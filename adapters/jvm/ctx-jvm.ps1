param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
$ErrorActionPreference = 'Stop'

# Selects one installed JDK or JRE by major version and runs java, javac or jar
# from it with JAVA_HOME set. User-level installs are searched before system
# ones so a developer's own choice wins for the same major version.
function Get-JavaHomes {
    $roots = @()
    if ($env:JAVA_HOME) { $roots += ,@($env:JAVA_HOME, 'java_home') }
    $userHome = [Environment]::GetFolderPath('UserProfile')
    $candidates = @(
        @((Join-Path $userHome '.jdks'), 'jdks'),
        @((Join-Path $userHome '.sdkman\candidates\java'), 'sdkman'),
        @((Join-Path $env:ProgramFiles 'Java'), 'system'),
        @((Join-Path $env:ProgramFiles 'Eclipse Adoptium'), 'system'),
        @((Join-Path $env:ProgramFiles 'Microsoft'), 'system'),
        @((Join-Path $env:ProgramFiles 'Amazon Corretto'), 'system'),
        @((Join-Path $env:ProgramFiles 'Zulu'), 'system')
    )
    foreach ($candidate in $candidates) {
        if (-not (Test-Path -LiteralPath $candidate[0] -PathType Container)) { continue }
        foreach ($entry in Get-ChildItem -LiteralPath $candidate[0] -Directory -ErrorAction SilentlyContinue) {
            $roots += ,@($entry.FullName, $candidate[1])
        }
    }
    $command = Get-Command java -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($command) { $roots += ,@((Split-Path -Parent (Split-Path -Parent $command.Source)), 'path') }
    $seen = @{}
    foreach ($root in $roots) {
        $path = $root[0]
        if (-not $path -or $seen.ContainsKey($path)) { continue }
        if (-not (Test-Path -LiteralPath (Join-Path $path 'bin\java.exe') -PathType Leaf)) { continue }
        $seen[$path] = $true
        [pscustomobject]@{ home = $path; source = $root[1] }
    }
}

function Get-JavaInventory {
    foreach ($entry in Get-JavaHomes) {
        $java = Join-Path $entry.home 'bin\java.exe'
        $output = (& $java -version 2>&1 | Out-String)
        if ($output -notmatch 'version "([^"]+)"') { continue }
        $version = $Matches[1]
        $major = if ($version.StartsWith('1.')) { $version.Split('.')[1] } else { ($version -split '[.+\-]')[0] }
        [pscustomobject]@{ selection = $major; home = $entry.home; version = $version; source = $entry.source }
    }
}

$operation = if ($Arguments.Count) { $Arguments[0] } else { '' }
$payload = @($Arguments | Select-Object -Skip 1)
$inventory = @(Get-JavaInventory)
# The first home wins when several installs share a major version.
$distinct = @($inventory | Group-Object selection | ForEach-Object { $_.Group[0] })
function Get-InstalledList { ($distinct | ForEach-Object { $_.selection }) -join ' ' }
function Find-Record($selection) { $distinct | Where-Object { $_.selection -eq $selection } | Select-Object -First 1 }

switch ($operation) {
    'list' { $distinct | ForEach-Object { $_.selection }; exit 0 }
    'observe' {
        $contexts = @($distinct | ForEach-Object {
            @{ selection = $_.selection; attributes = @{ kind = 'jvm'; isolation = 'host'; version = $_.version; home = $_.home; source = $_.source } }
        })
        @{ version = 1; contexts = $contexts } | ConvertTo-Json -Compress -Depth 5
        exit 0
    }
    'validate' {
        $selection = if ($payload.Count) { $payload[0] } else { '' }
        if (-not $selection -or -not (Find-Record $selection)) { [Console]::Error.WriteLine("jvm: Java $selection is not installed (installed: $(Get-InstalledList))"); exit 1 }
        exit 0
    }
    'doctor' {
        $selection = if ($payload.Count) { $payload[0] } else { '' }
        $record = if ($selection) { Find-Record $selection } else { $distinct | Select-Object -First 1 }
        if ($selection -and -not $record) { [Console]::Error.WriteLine("jvm: Java $selection is not installed (installed: $(Get-InstalledList))"); exit 1 }
        if (-not $record) { [Console]::Error.WriteLine('jvm: no Java installation found'); exit 127 }
        & (Join-Path $record.home 'bin\java.exe') -version
        exit $LASTEXITCODE
    }
    'run' {
        $selection = ''
        if ($payload.Count -and $payload[0] -ne '--') { $selection = $payload[0]; $payload = @($payload | Select-Object -Skip 1) }
        if ($payload.Count -and $payload[0] -eq '--') { $payload = @($payload | Select-Object -Skip 1) }
        $record = if ($selection) { Find-Record $selection } else { $distinct | Select-Object -First 1 }
        if ($selection -and -not $record) { [Console]::Error.WriteLine("jvm: Java $selection is not installed (installed: $(Get-InstalledList))"); exit 1 }
        if (-not $record) { [Console]::Error.WriteLine('jvm: no Java installation found'); exit 127 }
        $tool = if ($env:CTX_ADAPTER_COMMAND -in @('javac', 'jar')) { $env:CTX_ADAPTER_COMMAND } else { 'java' }
        $executable = Join-Path $record.home "bin\$tool.exe"
        if (-not (Test-Path -LiteralPath $executable -PathType Leaf)) { [Console]::Error.WriteLine("jvm: $tool is not part of the selected installation"); exit 127 }
        $env:JAVA_HOME = $record.home
        $env:PATH = (Join-Path $record.home 'bin') + [IO.Path]::PathSeparator + $env:PATH
        & $executable @payload
        exit $LASTEXITCODE
    }
    default { exit 2 }
}
