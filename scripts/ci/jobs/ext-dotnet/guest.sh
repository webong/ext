#!/usr/bin/env bash
# ext-dotnet/guest: the .NET guest SDK (pkg/ext-dotnet/ExtIO) against the fixtures and the Go suites.
. "$(dirname "$0")/../lib.sh"

(
scripts/lang/csharp.sh test
)
ci_import_env
