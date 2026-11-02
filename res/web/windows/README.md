# Ext.Web.Windows

`Ext.Web.Windows` binds an application-owned `CoreWebView2` to the portable
`res/web` workflows. `WebView2Workspace` registers and executes source already
prepared by the reviewed-userscript tooling, and exposes WebView2 profile
extension mechanics.

The binding does not parse userscripts, persist records, choose a profile, or
approve extension changes. Those decisions stay with the consumer. Installed
document scripts are removed when the workspace is disposed; extensions belong
to the selected WebView2 profile and outlive the workspace according to
WebView2's rules.

```powershell
dotnet build .\Ext.Web.Windows.csproj
dotnet run --project .\Tests\Ext.Web.Windows.Tests.csproj
```

## Install

`Ext.Web.Windows` is released as a NuGet package of that name, at the version of the `ext-dotnet-vX.Y.Z` tag
(see [module versioning](https://github.com/webong/ext/blob/main/docs/module-versioning.md)). The types are in the namespace `Webong.Ext.Web.Windows`.

```powershell
dotnet add package Ext.Web.Windows
```

The package is attached to the GitHub release of its tag with a checksum file and a provenance
attestation, and is pushed to a NuGet feed when the repository is configured with one.
