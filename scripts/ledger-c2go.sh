#!/usr/bin/env bash
# Translate and test the C ledger's portable store/recorder profile. No release.
set -euo pipefail
cd "$(dirname "$0")/.."
case "$(go env GOOS)/$(go env GOARCH)" in
  darwin/amd64|darwin/arm64|linux/amd64|linux/arm64) ;;
  *) echo "translated ledger: unsupported target" >&2; exit 1 ;;
esac
work=$(mktemp -d "${TMPDIR:-/tmp}/ext-ledger-c2go.XXXXXX")
trap 'rm -rf "$work"' EXIT
cmake -S lib/ledger -B "$work/native" -DEXT_LEDGER_PORTABLE=ON -DEXT_LEDGER_TESTS=ON -DEXT_LEDGER_COMMAND=OFF
cmake --build "$work/native" --parallel 3
ctest --test-dir "$work/native" --output-on-failure
(cd pkg/ext-go/c2goengine/tools && GOWORK=off go run . -library ledger -publish "$work/go")
# Public API tests exercise ledger/internal/ccgo through ledger.Bind.
# A missing test or an unexpected skip fails the gate.
go test -tags ext_ledger_c2go -count=1 -json ./pkg/ext-go/c2goengine/ledger | python3 scripts/check-go-test-results.py \
  TestTranslatedLedgerConformance TestTranslatedLedgerMemoryAndFingerprints \
  TestTranslatedLedgerRecorderFixtures TestTranslatedLedgerUnsupported TestTranslatedLedgerPeers
