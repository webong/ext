# Windows clipboard resource guest

This private Ext package guest exposes `ext.system.clipboard/v1` through the
native `Ext.Clipboard.Windows` library. It uses the .NET ExtIO guest SDK and is
run by Ext as an installed package; the executable is not a public command.

Build with `dotnet publish Ext.System.Clipboard.Guest.csproj -c Release -r
win-x64 --self-contained true -p:PublishSingleFile=true
-p:IncludeNativeLibrariesForSelfExtract=true`. Run `python3
scripts/release/pack-resource-guest.py ext.json PUBLISHED_EXE
NEW_PACKAGE_DIRECTORY windows amd64` from the repository root to seal it into an Ext
package and a matching zip archive. Install with `ext package install
PACKAGE_DIRECTORY` or `ext package install PACKAGE.zip --sha256 DIGEST`,
then `ext package trust system.clipboard.windows`. The guest hashes its exact
executable bytes for the plugin handshake, so the host verifies that revision.
