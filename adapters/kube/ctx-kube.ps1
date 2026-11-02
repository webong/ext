[CmdletBinding(PositionalBinding = $false)]
param(
    [Parameter(Position = 0)][string]$Operation,
    [Parameter(Position = 1)][string]$Selection,
    [Parameter(Position = 2, ValueFromRemainingArguments = $true)][string[]]$Arguments
)

$kubectl = Get-Command kubectl -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $kubectl) { [Console]::Error.WriteLine('kube: kubectl is not installed'); exit 127 }
$kubectlPath = $kubectl.Source

function Test-Context([string]$Name, [string]$Config) {
    $testArguments = @('config', 'get-contexts', $Name)
    if ($Config) { $testArguments = @('--kubeconfig', $Config) + $testArguments }
    & $kubectlPath @testArguments *> $null
    return $LASTEXITCODE -eq 0
}

switch ($Operation) {
    'list' { & $kubectlPath config get-contexts; exit $LASTEXITCODE }
    'configure' {
        if ($Arguments.Count -gt 0 -and $Arguments[0] -eq '--') { $Arguments = @($Arguments | Select-Object -Skip 1) }
        $namespace = ''; $kubeconfig = ''
        for ($index = 0; $index -lt $Arguments.Count; $index++) {
            $argument = $Arguments[$index]
            if ($argument -eq '--namespace') {
                if ($index + 1 -ge $Arguments.Count) { exit 2 }
                $index++; $namespace = $Arguments[$index]
            } elseif ($argument -like '--namespace=*') {
                $namespace = $argument.Substring(12)
            } elseif ($argument -eq '--kubeconfig') {
                if ($index + 1 -ge $Arguments.Count) { exit 2 }
                $index++; $kubeconfig = $Arguments[$index]
            } elseif ($argument -like '--kubeconfig=*') {
                $kubeconfig = $argument.Substring(13)
            } else {
                [Console]::Error.WriteLine("kube: unsupported selection option $argument"); exit 2
            }
        }
        if (-not (Test-Context $Selection $kubeconfig)) { [Console]::Error.WriteLine("kube: unknown context $Selection"); exit 1 }
        "kube_context`t$Selection"
        if ($namespace) { "kube_namespace`t$namespace" }
        if ($kubeconfig) { "kubeconfig`t$kubeconfig" }
    }
    'validate' {
        if (-not (Test-Context $Selection $env:EXT_ADAPTER_VALUE_KUBECONFIG)) { [Console]::Error.WriteLine("kube: unknown context $Selection"); exit 1 }
    }
    'doctor' {
        if ($Selection -and -not (Test-Context $Selection $env:EXT_ADAPTER_VALUE_KUBECONFIG)) { [Console]::Error.WriteLine("kube: unknown context $Selection"); exit 1 }
    }
    'run' {
        if ($Arguments.Count -gt 0 -and $Arguments[0] -eq '--') { $Arguments = @($Arguments | Select-Object -Skip 1) }
        $context = if ($env:EXT_ADAPTER_VALUE_KUBE_CONTEXT) { $env:EXT_ADAPTER_VALUE_KUBE_CONTEXT } else { $Selection }
        $namespace = $env:EXT_ADAPTER_VALUE_KUBE_NAMESPACE
        $kubeconfig = $env:EXT_ADAPTER_VALUE_KUBECONFIG
        $hasContext = $false; $hasNamespace = $false
        foreach ($argument in $Arguments) {
            if ($argument -eq '--context' -or $argument -like '--context=*') { $hasContext = $true }
            if ($argument -eq '--namespace' -or $argument -like '--namespace=*' -or $argument -eq '-n' -or $argument -like '-n?*') { $hasNamespace = $true }
        }
        if ($namespace -and -not $hasNamespace) { $Arguments = @('--namespace', $namespace) + $Arguments }
        if ($context -and -not $hasContext) { $Arguments = @('--context', $context) + $Arguments }
        if ($kubeconfig -and -not $env:KUBECONFIG) { $env:KUBECONFIG = $kubeconfig }
        & $kubectlPath @Arguments
        exit $LASTEXITCODE
    }
    default { exit 2 }
}
