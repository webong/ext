#!/usr/bin/env bash
# Build in the workspace; execute staged CLI/guests outside it with GOWORK=off.
# This checks runtime independence, not published Go-module availability.
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
cd "$repo"
python3 scripts/test_check_go_test_results.py
build_dir=$(mktemp -d "${TMPDIR:-/tmp}/ext-package-acceptance.XXXXXX")
build_dir=$(cd "$build_dir" && pwd -P)
# Native Go/C programs on Windows need Windows paths, not MSYS /tmp paths.
if command -v cygpath >/dev/null 2>&1; then
  build_dir=$(cygpath -m "$build_dir")
  repo=$(cygpath -m "$repo")
fi
trap 'rm -rf "$build_dir"' EXIT
cmake -S lib/plugin -B "$build_dir/engine" -DCMAKE_BUILD_TYPE=Release -DEXT_BUILD_SHARED=ON
cmake --build "$build_dir/engine" --target ext_plugin_host_shared ext_store_resolve_test ext_fixtures_test ext_interface_test ext_registry_test ext_fs_write_test --config Release -j 4
library_path=""
for candidate in libext_plugin_host.dylib libext_plugin_host.so libext_plugin_host.dll ext_plugin_host.dll Release/ext_plugin_host.dll; do
  if [[ -f "$build_dir/engine/$candidate" ]]; then library_path="$build_dir/engine/$candidate"; break; fi
done
[[ -n "$library_path" ]] || { echo "Required C engine library missing" >&2; exit 1; }
export EXT_ACCEPTANCE_LIBRARY="$library_path"
cmake -S lib/ledger -B "$build_dir/ledger" -DCMAKE_BUILD_TYPE=Release -DEXT_LEDGER_SHARED=ON -DEXT_LEDGER_TESTS=OFF
cmake --build "$build_dir/ledger" --target ext_ledger_shared --config Release -j 4
ledger_path=""
for candidate in libext_ledger.dylib libext_ledger.so libext_ledger.dll ext_ledger.dll Release/ext_ledger.dll; do
  if [[ -f "$build_dir/ledger/$candidate" ]]; then ledger_path="$build_dir/ledger/$candidate"; break; fi
done
[[ -n "$ledger_path" ]] || { echo "Required C ledger library missing" >&2; exit 1; }
export EXT_LEDGER_LIBRARY="$ledger_path"
go test ./pkg/ext-go/engine/ledger/supervisor -run '^TestNativeSupervisor$' -count=1 -json | python3 scripts/check-go-test-results.py TestNativeSupervisor
# Test the same described ledger through a linked C driver. No fallback allowed.
ledger_link="$ledger_path"
case "$(go env GOOS)" in
  darwin|linux) ledger_link="$ledger_link -Wl,-rpath,$build_dir/ledger" ;;
esac
CGO_ENABLED=1 CGO_CFLAGS="-I$repo/lib/ledger/include" CGO_LDFLAGS="$ledger_link" go test -tags ext_ledger_cgo ./pkg/ext-go/cgoengine/ledger -run '^TestLinkedLedgerConformance$' -count=1 -json | python3 scripts/check-go-test-results.py TestLinkedLedgerConformance
EXT_PLUGIN_LIBRARY="$library_path" go test ./cmd/ext/internal/nativeplugin -run '^TestGraphPublicationOwnership$' -count=1 -json | python3 scripts/check-go-test-results.py TestGraphPublicationOwnership
go test ./pkg/ext-go/engine/ledger -run '^(TestNativeRecorderAndInventory|TestNativeProcessAndCancellation|TestNativeClose|TestNativeStoreConformance)$' -count=1 -json | python3 scripts/check-go-test-results.py TestNativeRecorderAndInventory TestNativeProcessAndCancellation TestNativeClose TestNativeStoreConformance TestNativeStoreConformance/memory TestNativeStoreConformance/file
go test ./src/ctx/internal/graphapi -count=1 -json | python3 scripts/check-go-test-results.py TestLegacyGraphMigration TestMigrationFailurePreservesLegacy
EXT_PLUGIN_LIBRARY="$library_path" go test ./src/ctx/internal/app -run '^(TestCTXRecordsSystemContextAndGraphCommandReadsIt|TestGraphProcessCLIValidation)$' -count=1 -json | python3 scripts/check-go-test-results.py TestCTXRecordsSystemContextAndGraphCommandReadsIt TestGraphProcessCLIValidation
EXT_PLUGIN_LIBRARY="$library_path" EXT_LEDGER_LIBRARY="$ledger_path" go test ./tests/crosslang -run '^TestExtGraph(AcrossLibraries|Reconciliation|LiveProcess|ProcessIdentity)$' -count=1 -json | python3 scripts/check-go-test-results.py TestExtGraphAcrossLibraries TestExtGraphReconciliation TestExtGraphReconciliation/memory TestExtGraphReconciliation/file TestExtGraphLiveProcess TestExtGraphProcessIdentity
EXT_PLUGIN_LIBRARY="$library_path" go test ./tests/crosslang -run '^TestReviewedPackageSnapshot$' -count=1 -json | python3 scripts/check-go-test-results.py TestReviewedPackageSnapshot
resolve_test=""
for candidate in ext_store_resolve_test ext_store_resolve_test.exe Release/ext_store_resolve_test.exe; do
  if [[ -f "$build_dir/engine/$candidate" ]]; then resolve_test="$build_dir/engine/$candidate"; break; fi
done
[[ -n "$resolve_test" ]] || { echo "Required C entrypoint test missing" >&2; exit 1; }
mkdir "$build_dir/resolve"
"$resolve_test" "$build_dir/resolve"
fixtures_test="$(dirname "$resolve_test")/ext_fixtures_test"
[[ -f "$fixtures_test" ]] || fixtures_test="$fixtures_test.exe"
"$fixtures_test" "$repo/lib/plugin/testdata/v1"
interface_test="$(dirname "$resolve_test")/ext_interface_test"
[[ -f "$interface_test" ]] || interface_test="$interface_test.exe"
"$interface_test" "$build_dir/interface"
registry_test="$(dirname "$resolve_test")/ext_registry_test"
[[ -f "$registry_test" ]] || registry_test="$registry_test.exe"
"$registry_test" "$repo/lib/plugin/testdata/v1/registry-cases.json"
go test ./pkg/ext-go/engine/plugin/host/store -run '^TestResolveEntrypoint' -count=1
resource_cases=()
if [[ "$(go env GOOS)" != windows ]]; then
  resource_cases=(TestAdapterAcrossProducts/nested-adapter-snapshots TestResourceDiscovery/cancel-list TestResourceDiscovery/cancel-call TestResourceDependencyGraph/cancel-chain)
fi
go test ./cmd/ext -run '^(TestEndToEnd|TestAdapterAcrossProducts|TestLegacyAdapterPlan|TestAdapterDependencyPlan|TestProviderThroughCLibrary|TestPackagePlan|TestBundleLifecycle|TestResourceDiscovery|TestResourceDependencyGraph)$' -count=1 -json -timeout=5m | python3 scripts/check-go-test-results.py \
  TestEndToEnd TestAdapterAcrossProducts TestLegacyAdapterPlan TestAdapterDependencyPlan TestProviderThroughCLibrary \
  TestPackagePlan TestBundleLifecycle TestResourceDiscovery TestResourceDependencyGraph TestResourceDependencyGraph/startup-failure ${resource_cases[@]+"${resource_cases[@]}"}
go test ./cmd/ext/internal/resourcecmd -count=1 -json | python3 scripts/check-go-test-results.py \
  TestCanceledRequestDoesNotOpenStore TestDependencyGrants
native_graph_cases=()
if [[ "$(go env GOOS)" != windows ]]; then native_graph_cases=(TestCGraphCloseDuringStartup); fi
EXT_PLUGIN_LIBRARY="$library_path" go test ./cmd/ext/internal/nativeplugin -count=1 -json | python3 scripts/check-go-test-results.py TestGraphSnapshotCopyFailureCleansPrivateStore ${native_graph_cases[@]+"${native_graph_cases[@]}"}
EXT_PLUGIN_LIBRARY="$library_path" go test ./cmd/ext/internal/pkgcmd -run '^(TestDiscoveredCommandsRunFromPrivateSnapshots|TestCommandSnapshotSurvivesSourceRemoval)$' -count=1 -json | python3 scripts/check-go-test-results.py TestDiscoveredCommandsRunFromPrivateSnapshots TestDiscoveredCommandsRunFromPrivateSnapshots/stdio TestDiscoveredCommandsRunFromPrivateSnapshots/socket TestCommandSnapshotSurvivesSourceRemoval
go test ./cmd/ext/internal/pkgcmd -run '^(TestManageProviderLifecycle|TestMigration.*)$' -count=1 -v -timeout=5m

go test ./cmd/ext/internal/sourcecmd -count=1 -v -timeout=2m

go test ./cmd/ext/internal/bundlecmd -count=1 -json -timeout=2m | python3 scripts/check-go-test-results.py \
  TestInterruptedPublicationReconcilesEveryPackage TestNativeUpgradeRecovery \
  TestNativeUpgradeProcessLoss TestNativeBundleInstallAndReplay TestNativeBundlePlanDoesNotFetch

go test ./src/ctx/internal/adapterenv -count=1 -json | python3 scripts/check-go-test-results.py TestDeclaredDependenciesReplaceInheritedHints TestHostDispatcherReplacesInheritedHints
if [[ "$(go env GOOS)" != windows ]]; then
  go test ./res/system/service/credential/client -count=1 -json | python3 scripts/check-go-test-results.py TestGetDeclaredUsesHostProtocol TestGetDeclaredUsesProductPrefix
fi
# Browser fixtures execute POSIX shell guests; require every selected case there.
if [[ "$(go env GOOS)" != windows ]]; then
  EXT_PLUGIN_LIBRARY="$library_path" go test ./src/ctx/internal/app/browserhost -count=1 -json | python3 scripts/check-go-test-results.py \
    TestBrowserInvocationSnapshotAndReadmission TestBrowserSnapshotCancellationCleanup TestBrowserInvocationUsesDeclaredDependencies \
    TestQuerySourceOrderFallbackAndTrust TestQueryCancellationAndMalformedSources \
    TestNormalizeDispatchAndPortableRoundTrip TestNormalizeRejectsInvalidOptionsResponsesAndTimeout
fi

# Shared writer coordination, including product-owned catalog setup helpers.
go test ./res/system/service/host/dirlock ./cmd/ext/internal/packagelock -count=1
go test ./src/ctn/internal/app -run '^(TestProviderMutationsCoordinateWithPackageWriter|TestProviderSessionsCoordinateWithPackageWriter|TestInferenceProviderRequiresTrustedPackage|TestPerceptionProviderPackageTrustAndCall|TestModelRunThroughTrustedProviderPackage)$' -count=1 -json | python3 scripts/check-go-test-results.py \
  TestProviderMutationsCoordinateWithPackageWriter TestProviderSessionsCoordinateWithPackageWriter TestPerceptionProviderPackageTrustAndCall/cancel-owned-session TestInferenceProviderRequiresTrustedPackage TestPerceptionProviderPackageTrustAndCall TestModelRunThroughTrustedProviderPackage
EXT_PLUGIN_LIBRARY="$library_path" go test ./src/ctx/internal/app -run '^(TestCatalogWritersRespectPackageLock|TestCatalogAddInstallsPackageDependency)$' -count=1 -json | python3 scripts/check-go-test-results.py TestCatalogWritersRespectPackageLock TestCatalogAddInstallsPackageDependency

go test ./pkg/ext-go/engine/plugin/host/adapter -run '^(TestProjectDependencies|TestDeclaredShareDependencies|TestPackageShareDependenciesTarget)$' -count=1 -json | python3 scripts/check-go-test-results.py TestProjectDependencies TestDeclaredShareDependencies TestPackageShareDependenciesTarget
go test ./src/ctx/internal/arch -run '^TestEveryAdapterSourceLoadsOnEveryPlatform$' -count=1 -json | python3 scripts/check-go-test-results.py TestEveryAdapterSourceLoadsOnEveryPlatform

fs_test="$(dirname "$resolve_test")/ext_fs_write_test"
[[ -f "$fs_test" ]] || fs_test="$fs_test.exe"
"$fs_test" "$build_dir/writer-fs"
EXT_PLUGIN_BUILD="$(dirname "$library_path")" go test ./pkg/ext-go/cengine/library -run '^TestDescribedPackageWriter$' -count=1 -timeout=2m
