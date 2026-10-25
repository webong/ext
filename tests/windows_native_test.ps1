[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$testRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("ctx-windows-test-" + [guid]::NewGuid().ToString('N'))
$bin = Join-Path $testRoot 'bin'
$config = Join-Path $testRoot 'config'
$fakeBin = Join-Path $testRoot 'fake-bin'
$fallbackBin = Join-Path $testRoot 'fallback-bin'
$project = Join-Path $testRoot 'project'

function Assert-Success([string]$Description) {
    if ($LASTEXITCODE -ne 0) { throw "$Description failed with exit code $LASTEXITCODE" }
}

function Assert-Output([string]$Description, [string]$Expected, [object[]]$Actual) {
    $joined = ($Actual -join "`n").Trim()
    if ($joined -ne $Expected) { throw "$Description output was '$joined'; expected '$Expected'" }
}

try {
    New-Item -ItemType Directory -Force -Path $fakeBin, $fallbackBin, $project | Out-Null

    $parseFailures = @()
    Get-ChildItem (Join-Path $root 'adapters') -Recurse -Filter '*.ps1' | ForEach-Object {
        $tokens = $null; $errors = $null
        [System.Management.Automation.Language.Parser]::ParseFile($_.FullName, [ref]$tokens, [ref]$errors) | Out-Null
        if ($errors.Count -gt 0) { $parseFailures += "$($_.FullName): $($errors -join '; ')" }
    }
    if ($parseFailures.Count -gt 0) { throw ($parseFailures -join "`n") }

    $coreBin = Join-Path $testRoot 'core-bin'
    $coreConfig = Join-Path $testRoot 'core-config'
    & (Join-Path $root 'install.ps1') -BinDir $coreBin -ConfigDir $coreConfig
    Assert-Success 'core-only source installation'
    if (Test-Path (Join-Path $coreConfig 'catalog\adapters\docker')) { throw 'default Windows install copied an adapter catalog' }
    if (Test-Path (Join-Path $coreConfig 'adapters\docker')) { throw 'default Windows install activated Docker' }

    $extBin = Join-Path $testRoot 'ext-bin'
    $extConfig = Join-Path $testRoot 'ext-config'
    & (Join-Path $root 'install.ps1') -BinDir $extBin -ConfigDir $extConfig -Ext
    Assert-Success 'ext source installation'
    $extVersion = (& (Join-Path $extBin 'ext.exe') version | Select-Object -First 1)
    if ($extVersion -notmatch '^ext ') { throw "ext.exe version printed: $extVersion" }
    $ctxViaExt = (& (Join-Path $extBin 'ext.exe') ctx version | Select-Object -First 1)
    if ($ctxViaExt -notmatch '^ctx ') { throw "ext ctx version printed: $ctxViaExt" }

    & (Join-Path $root 'install.ps1') -BinDir $bin -ConfigDir $config -AllAdapters
    Assert-Success 'source installation'
    $completionTokens = $null; $completionErrors = $null
    [System.Management.Automation.Language.Parser]::ParseFile((Join-Path $config 'ctx-completion.ps1'), [ref]$completionTokens, [ref]$completionErrors) | Out-Null
    if ($completionErrors.Count -gt 0) { throw "Generated completion has syntax errors: $($completionErrors -join '; ')" }

    @'
@echo off
if "%1 %2"=="configure list-profiles" (
  echo client-a
  exit /b 0
)
echo %*
'@ | Set-Content -Encoding ASCII (Join-Path $fakeBin 'aws.cmd')
    @'
@echo off
if "%1"=="config" (
  echo client-a
  exit /b 0
)
echo %*
'@ | Set-Content -Encoding ASCII (Join-Path $fakeBin 'gcloud.cmd')
    @'
@echo off
if "%1 %2"=="config get-contexts" (
  exit /b 0
)
echo %*
'@ | Set-Content -Encoding ASCII (Join-Path $fakeBin 'kubectl.cmd')
    @'
@echo off
echo PGSERVICE=%PGSERVICE% %*
'@ | Set-Content -Encoding ASCII (Join-Path $fakeBin 'psql.cmd')
    @'
@echo off
echo %*
'@ | Set-Content -Encoding ASCII (Join-Path $fakeBin 'mysql.cmd')
    @'
@echo off
echo %*
'@ | Set-Content -Encoding ASCII (Join-Path $fakeBin 'firefox.cmd')
    @'
@echo off
if "%1 %2"=="context ls" (
  echo alpha
  exit /b 0
)
if "%1 %2"=="context inspect" exit /b 0
echo %*
'@ | Set-Content -Encoding ASCII (Join-Path $fakeBin 'docker.cmd')

    # Keep duplicate application names on PATH. Adapters must choose the first
    # executable, rather than combining all discovered paths into one command.
    foreach ($command in @('aws', 'gcloud', 'kubectl', 'psql', 'mysql', 'firefox')) {
        Copy-Item (Join-Path $bin 'ctx.exe') (Join-Path $fallbackBin "$command.exe")
    }

    $env:CTX_HOME = $config
    $env:CTX_BIN_DIR = $bin
    $env:APPDATA = Join-Path $testRoot 'AppData\Roaming'
    $env:LOCALAPPDATA = Join-Path $testRoot 'AppData\Local'
    $env:PATH = "$bin;$fakeBin;$fallbackBin;$env:PATH"
    foreach ($command in @('aws', 'gcloud', 'kubectl', 'psql', 'mysql', 'firefox')) {
        $applications = @(Get-Command $command -CommandType Application -ErrorAction Stop)
        if ($applications.Count -lt 2) { throw "duplicate PATH fixture missing for $command" }
        if ($applications[0].Source -ne (Join-Path $fakeBin "$command.cmd")) {
            throw "first PATH candidate for $command is not the test executable"
        }
    }
    $profilesDirectory = Join-Path $env:APPDATA 'Mozilla\Firefox'
    New-Item -ItemType Directory -Force -Path $profilesDirectory | Out-Null
    "[Profile0]`r`nName=client-a`r`n" | Set-Content -Encoding ASCII (Join-Path $profilesDirectory 'profiles.ini')

    Push-Location $project
    try {
        $ctx = Join-Path $bin 'ctx.exe'
        $completion = @(& $ctx completion powershell) -join "`n"
        Assert-Success 'PowerShell completion generation'
        if ($completion -notmatch 'Register-ArgumentCompleter') { throw 'PowerShell completion output is incomplete' }
        & $ctx set aws client-a | Out-Null; Assert-Success 'AWS selection'
        & $ctx set gcloud client-a | Out-Null; Assert-Success 'gcloud selection'
        & $ctx set kube production --namespace payments | Out-Null; Assert-Success 'Kubernetes selection'
        & $ctx set postgres client-a-dev | Out-Null; Assert-Success 'PostgreSQL selection'
        & $ctx set mysql client-a | Out-Null; Assert-Success 'MySQL selection'
        & $ctx set browser firefox:client-a | Out-Null; Assert-Success 'browser selection'
        & $ctx set docker alpha | Out-Null; Assert-Success 'Docker selection'

        Assert-Output 'AWS routing' '--profile client-a sts get-caller-identity' @(& $ctx run aws sts get-caller-identity)
        Assert-Success 'AWS routing'
        Assert-Output 'gcloud routing' '--configuration client-a projects list' @(& $ctx run gcloud projects list)
        Assert-Success 'gcloud routing'
        Assert-Output 'Kubernetes routing' '--context production --namespace payments get pods' @(& $ctx run kubectl get pods)
        Assert-Success 'Kubernetes routing'
        Assert-Output 'PostgreSQL routing' 'PGSERVICE=client-a-dev app' @(& $ctx run psql app)
        Assert-Success 'PostgreSQL routing'
        Assert-Output 'MySQL routing' '--login-path=client-a app' @(& $ctx run mysql app)
        Assert-Success 'MySQL routing'
        Assert-Output 'browser listing' 'firefox:client-a' @(& $ctx ls browser)
        Assert-Success 'browser listing'
        Assert-Output 'browser launch' '-P client-a https://example.test' @(& $ctx open https://example.test)
        Assert-Success 'browser launch'
        Assert-Output 'Docker routing' '--context alpha ps' @(& $ctx run docker ps)
        Assert-Success 'Docker routing'
        & $ctx clear docker | Out-Null; Assert-Success 'Docker selection clear'
        Assert-Output 'Docker native-default routing' 'ps' @(& $ctx run docker ps)
        Assert-Success 'Docker native-default routing'
        & $ctx set docker alpha | Out-Null; Assert-Success 'Docker selection restore'
        if (@(& $ctx ls manager) -notcontains 'docker:alpha') { throw 'manager provider listing did not include docker:alpha' }
        Assert-Success 'manager provider listing'

        # AllAdapters also activates tools the synthetic fixture does not
        # provide. Keep the full installation check, then diagnose only the
        # adapters backed by fake executables rather than the runner's tools.
        $fixtureAdapters = @('aws', 'gcloud', 'kube', 'postgres', 'mysql', 'firefox', 'docker')
        Get-ChildItem (Join-Path $config 'adapters') -Directory | ForEach-Object {
            if ($fixtureAdapters -notcontains $_.Name) {
                & $ctx adapter remove $_.Name | Out-Null
                Assert-Success "remove unexercised adapter $($_.Name)"
            }
        }
        & $ctx doctor; Assert-Success 'ctx doctor'
    }
    finally {
        Pop-Location
    }

    Write-Host 'ctx native Windows tests passed'
}
finally {
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $testRoot
}
