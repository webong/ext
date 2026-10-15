package adapter

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"unicode/utf16"
)

// powershellArguments passes the adapter protocol as literal string values.
// Windows PowerShell -File interprets forwarded CLI flags (including --) as
// named script parameters, even with ValueFromRemainingArguments enabled.
func powershellArguments(path string, args []string) []string {
	literal := func(value string) string {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	values := make([]string, len(args))
	for i, arg := range args {
		values[i] = literal(arg)
	}
	script := strings.Join([]string{
		"$ProgressPreference = 'SilentlyContinue'",
		"[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)",
		"$OutputEncoding = [Console]::OutputEncoding",
		"$ctxInvocationArguments = @(" + strings.Join(values, ",") + ")",
		// Windows PowerShell serializes uncaught errors as CLIXML for an
		// encoded command, even with OutputFormat Text. Emit caught failures
		// directly so adapter diagnostics remain readable.
		"try { & " + literal(path) + " @ctxInvocationArguments; " +
			"if (-not $?) { if ($LASTEXITCODE) { exit $LASTEXITCODE }; exit 1 }; exit $LASTEXITCODE " +
			"} catch { [Console]::Error.WriteLine($_.ToString()); exit 1 }",
	}, "; ")
	units := utf16.Encode([]rune(script))
	encoded := make([]byte, 2*len(units))
	for i, unit := range units {
		binary.LittleEndian.PutUint16(encoded[i*2:], unit)
	}
	return []string{"-NoLogo", "-NoProfile", "-OutputFormat", "Text", "-EncodedCommand", base64.StdEncoding.EncodeToString(encoded)}
}
