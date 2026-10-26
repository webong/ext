using Microsoft.Web.WebView2.Core;
using Webong.Ext.Web.Windows;

internal static class WebView2WorkspaceTests
{
    private static async Task<int> Main()
    {
        try
        {
            await Run();
            Console.WriteLine("WebView2 workspace tests passed");
            return 0;
        }
        catch (Exception error)
        {
            Console.Error.WriteLine(error);
            return 1;
        }
    }

    private static async Task Run()
    {
        var view = new CoreWebView2();
        await using var workspace = new WebView2Workspace(view);
        const string prepared = "(() => { window.extReviewed = true; return true; })()";
        var script = await workspace.InstallDocumentScriptAsync("reading", prepared);
        Assert(script.Id == "reading" && view.Scripts.Count == 1, "document script was not registered");
        Assert(await workspace.ExecuteDocumentScriptNowAsync("reading") == "true", "script did not execute");
        Assert(view.LastExecutedScript == prepared, "workspace changed prepared userscript source");
        workspace.RemoveDocumentScript("reading");
        Assert(view.Scripts.Count == 0, "script was not removed");

        var rejected = false;
        try { await workspace.InstallDocumentScriptAsync("bad id", prepared); }
        catch (ArgumentException) { rejected = true; }
        Assert(rejected, "invalid userscript identity was accepted");

        var extensionDirectory = Path.Combine(Path.GetTempPath(), "ext-webview2-test-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(extensionDirectory);
        try
        {
            File.WriteAllText(Path.Combine(extensionDirectory, "manifest.json"), "{}");
            var installed = await workspace.InstallExtensionAsync(extensionDirectory);
            Assert(installed.Id == "test-id" && installed.Enabled, "extension was not installed");
            await workspace.SetExtensionEnabledAsync(installed.Id, false);
            Assert(!(await workspace.ListExtensionsAsync())[0].Enabled, "extension was not disabled");
            await workspace.RemoveExtensionAsync(installed.Id);
            Assert((await workspace.ListExtensionsAsync()).Count == 0, "extension was not removed");
        }
        finally
        {
            Directory.Delete(extensionDirectory, true);
        }
    }

    private static void Assert(bool condition, string message)
    {
        if (!condition) throw new InvalidOperationException(message);
    }
}
