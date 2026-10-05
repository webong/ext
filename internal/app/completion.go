package app

import (
	"fmt"
	"io"
)

func completion(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "powershell" {
		fmt.Fprintln(stderr, "ctx: completion currently supports powershell")
		return 2
	}
	fmt.Fprint(stdout, powershellCompletion)
	return 0
}

const powershellCompletion = `# ctx PowerShell completion
Register-ArgumentCompleter -Native -CommandName ctx -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)

    $words = @($commandAst.CommandElements | ForEach-Object { $_.Extent.Text })
    $commands = @('status', 'resolve', 'explain', 'env', 'set', 'clear', 'profile', 'adapter', 'computer', 'manager', 'browser', 'credential', 'setup', 'ls', 'open', 'doctor', 'build', 'share:manager', 'share:browser', 'share:computer', 'share:credential', 'graph', 'hook', 'plugin', 'run', 'shell', 'real', 'completion', 'version')

    function Emit-CtxCompletion([string[]]$values) {
        $values | Where-Object { $_ -and $_ -like "$wordToComplete*" } | Sort-Object -Unique | ForEach-Object {
            [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
        }
    }
    function Get-CtxSelectors {
        $values = @('browser')
        $values += @(& ctx adapter ls 2>$null | ForEach-Object { ($_ -split '\s+')[0] })
        return $values
    }
    function Get-CtxManagerProviders {
        @(& ctx adapter ls manager 2>$null | ForEach-Object { ($_ -split '\s+')[0] })
    }
    function Get-CtxManagerEndpoints {
        $values = @(& ctx ls manager 2>$null)
        $values += @(& ctx manager ls 2>$null | ForEach-Object { ($_ -split '\s+')[0] })
        return $values
    }
    function Get-CtxComputerAdapters {
        @(& ctx adapter ls computer 2>$null | ForEach-Object { ($_ -split '\s+')[0] })
    }
    function Get-CtxAvailableAdapters {
        @(& ctx adapter available 2>$null | Where-Object { $_ -match '\savailable\s' } | ForEach-Object { ($_ -split '\s+')[0] })
    }
    function Get-CtxProfiles { @(& ctx profile ls 2>$null) }
    if ($words.Count -le 1) { Emit-CtxCompletion $commands; return }
    $subcommand = $words[1]
    if ($words.Count -eq 2 -and $commands -notcontains $subcommand) { Emit-CtxCompletion $commands; return }

    switch ($subcommand) {
        { $_ -in @('status', 'ls', 'set') } {
            if ($words.Count -le 2) { Emit-CtxCompletion (Get-CtxSelectors); return }
            if ($subcommand -eq 'set' -and $words.Count -le 3) {
                Emit-CtxCompletion @(& ctx ls $words[2] 2>$null); return
            }
        }
        'clear' { if ($words.Count -le 2) { Emit-CtxCompletion ((Get-CtxSelectors) + 'profile'); return } }
        'completion' { if ($words.Count -le 2) { Emit-CtxCompletion @('powershell'); return } }
        'graph' {
            if ($words.Count -le 2) { Emit-CtxCompletion @('scan', 'shells', 'filesystems', 'webviews', 'processes', 'process', 'resolve', 'status', 'vertices', 'edges', 'snapshot', 'changes'); return }
            if ($words[2] -eq 'resolve') {
                if ($words.Count -le 3) { Emit-CtxCompletion @('all', 'browser', 'computer', 'manager', 'shell', 'filesystem', 'webview'); return }
                if ($words[3] -eq 'shell') { Emit-CtxCompletion @('--name', '--select', '--max-age'); return }
                if ($words[3] -eq 'filesystem') { Emit-CtxCompletion @('--path', '--type', '--writable', '--min-free', '--select', '--max-age'); return }
                if ($words[3] -eq 'webview') { Emit-CtxCompletion @('--engine', '--api', '--abi', '--version', '--arch', '--select', '--max-age'); return }
                if ($words[-2] -eq '--supports') { Emit-CtxCompletion @('virtualizer', 'container'); return }
                Emit-CtxCompletion @('--supports'); return
            }
        }
        'hook' {
            if ($words.Count -le 2) { Emit-CtxCompletion @('bash', 'zsh', 'powershell', 'computer'); return }
            if ($words[2] -eq 'computer' -and $words.Count -le 3) { Emit-CtxCompletion (Get-CtxComputerAdapters); return }
        }
        'plugin' {
            if ($words.Count -le 2) { Emit-CtxCompletion @('computer'); return }
            if ($words[2] -eq 'computer' -and $words.Count -le 3) { Emit-CtxCompletion (Get-CtxComputerAdapters); return }
        }
        'computer' {
            if ($words.Count -le 2) { Emit-CtxCompletion @('hooks'); return }
            if ($words[2] -eq 'hooks' -and $words.Count -le 3) { Emit-CtxCompletion @('print', 'install', 'remove'); return }
            if ($words[2] -eq 'hooks' -and $words.Count -le 4) { Emit-CtxCompletion (Get-CtxComputerAdapters); return }
            if ($words[2] -eq 'hooks') { Emit-CtxCompletion @('--events', '--handler'); return }
        }
        'browser' { if ($words.Count -le 3) { Emit-CtxCompletion @('manage') } }
        { $_ -in @('credential', 'share:credential') } {
            if ($words.Count -le 2) { Emit-CtxCompletion @('get', 'put', 'copy'); return }
            if ($words[2] -eq 'get') { Emit-CtxCompletion @('--to-file', '--stdout'); return }
            if ($words[2] -eq 'put') { Emit-CtxCompletion @('--from-file', '--stdin', '--replace'); return }
            if ($words[2] -eq 'copy') { Emit-CtxCompletion @('--replace'); return }
        }
        'share:manager' {
            if ($words.Count -le 2) { Emit-CtxCompletion @('image', 'volume'); return }
            if ($words[2] -eq 'image' -and $words.Count -le 3) { Emit-CtxCompletion @('sync', 'copy'); return }
            if ($words[2] -eq 'volume' -and $words.Count -le 3) { Emit-CtxCompletion @('export', 'import', 'copy'); return }
            if ($words.Count -in @(4, 5)) { Emit-CtxCompletion (Get-CtxManagerEndpoints); return }
        }
        'share:browser' {
            if ($words.Count -le 2) { Emit-CtxCompletion @('cookie', 'policy', 'certificate', 'capabilities'); return }
            if ($words[2] -eq 'cookie' -and $words.Count -le 3) { Emit-CtxCompletion @('list', 'query', 'normalize', 'copy', 'import'); return }
            if ($words[2] -eq 'cookie' -and $words[3] -eq 'normalize') { Emit-CtxCompletion @('--from', '--store-id', '--from-file', '--stdin', '--to-file', '--stdout', '--timeout'); return }
            if ($words[3] -eq 'list') { Emit-CtxCompletion @('--from', '--site'); return }
            if ($words[3] -eq 'copy') { Emit-CtxCompletion @('--from', '--site', '--name', '--domain', '--path', '--id', '--ref', '--attribute', '--to-profile', '--to-file', '--stdout', '--replace'); return }
            if ($words[2] -eq 'cookie' -and $words[3] -eq 'query') {
                if ($words[-2] -eq '--format') { Emit-CtxCompletion @('json', 'header', 'netscape'); return }
                Emit-CtxCompletion @('--from', '--browser', '--site', '--name', '--mode', '--format', '--inline-file', '--inline-stdin', '--fallback-file', '--fallback-stdin', '--inline-only', '--all-hosts', '--include-expired', '--to-file', '--stdout', '--strict', '--require-match', '--timeout'); return
            }
            if ($words[2] -eq 'cookie' -and $words[3] -eq 'import') { Emit-CtxCompletion @('--from-file', '--stdin', '--to-profile', '--site', '--name', '--domain', '--path', '--attribute', '--replace'); return }
        }
        'build' { if ($words.Count -le 2) { Emit-CtxCompletion ((Get-CtxManagerProviders) + @(& ctx manager ls 2>$null | ForEach-Object { ($_ -split '\s+')[0] }) + '--cache-ref'); return } }
        'manager' {
            if ($words.Count -le 2) { Emit-CtxCompletion @('add', 'ls', 'show', 'apps', 'app', 'doctor', 'remove'); return }
            if ($words[2] -in @('show', 'doctor', 'remove') -and $words.Count -le 3) { Emit-CtxCompletion @((& ctx manager ls 2>$null | ForEach-Object { ($_ -split '\s+')[0] }) + @(& ctx manager apps 2>$null)); return }
            if ($words[2] -eq 'app' -and $words.Count -le 3) { Emit-CtxCompletion @(& ctx manager apps 2>$null); return }
            if ($words[2] -eq 'app' -and $words.Count -le 4) { Emit-CtxCompletion @('status', 'start', 'stop', 'doctor'); return }
            if ($words[2] -eq 'add' -and $words.Count -ge 4) { Emit-CtxCompletion @('--virtualizer', '--machine', '--provider', '--selection', '--address', '--command', '--plugin-dir', '--plugin', '--offline'); return }
        }
        'profile' {
            $operations = @('ls', 'show', 'use', 'set', 'unset', 'env', 'env-unset', 'clear')
            if ($words.Count -le 2) { Emit-CtxCompletion $operations; return }
            if ($words[2] -in @('show', 'use', 'set', 'unset', 'env', 'env-unset') -and $words.Count -le 3) {
                Emit-CtxCompletion (Get-CtxProfiles); return
            }
        }
        'adapter' {
            $operations = @('ls', 'available', 'add', 'refresh', 'inspect', 'build', 'pack', 'index', 'install', 'trust', 'test', 'doctor', 'remove')
            if ($words.Count -le 2) { Emit-CtxCompletion $operations; return }
            if ($words[2] -in @('inspect', 'trust', 'doctor', 'remove') -and $words.Count -le 3) {
                Emit-CtxCompletion @(& ctx adapter ls 2>$null | ForEach-Object { ($_ -split '\s+')[0] }); return
            }
            if ($words[2] -eq 'add') { Emit-CtxCompletion (Get-CtxAvailableAdapters); return }
        }
        'setup' { if ($words.Count -le 2) { Emit-CtxCompletion @('adapters', '--all', '--minimal', '--adapters'); return } }
        'shell' { if ($words.Count -le 2) { Emit-CtxCompletion @('--shell', '--'); return } }
    }
}
`
