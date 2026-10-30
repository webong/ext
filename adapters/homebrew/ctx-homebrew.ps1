param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
$ErrorActionPreference = 'Stop'

# Homebrew does not run on Windows, so this adapter reports nothing there.
$operation = if ($Arguments.Count) { $Arguments[0] } else { '' }
switch ($operation) {
    'list' { exit 0 }
    'observe' { '{"version":1,"contexts":[]}'; exit 0 }
    'programs' { '{"version":1,"programs":[]}'; exit 0 }
    'validate' { [Console]::Error.WriteLine('homebrew: Homebrew is not installed'); exit 1 }
    'doctor' { [Console]::Error.WriteLine('homebrew: Homebrew is not installed'); exit 127 }
    'run' { [Console]::Error.WriteLine('homebrew: Homebrew is not installed'); exit 127 }
    default { exit 2 }
}
