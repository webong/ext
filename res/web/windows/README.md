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
