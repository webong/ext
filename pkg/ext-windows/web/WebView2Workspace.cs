using Microsoft.Web.WebView2.Core;

namespace Webong.Ext.Web.Windows;

public sealed record BrowserExtensionInfo(string Id, string Name, bool Enabled);

public sealed record DocumentScriptInfo(string Id, string NativeId);

/// <summary>
/// In-process binding for an application-owned CoreWebView2 and its selected
/// profile. Call every member on the WebView2 UI thread. The caller owns
/// userscript review/persistence, extension approval, and workspace lifetime.
/// </summary>
public sealed class WebView2Workspace : IAsyncDisposable
{
    private sealed record InstalledScript(string Source, string NativeId);

    private readonly CoreWebView2 webView;
    private readonly Dictionary<string, InstalledScript> scripts = new(StringComparer.Ordinal);
    private readonly HashSet<string> pendingScripts = new(StringComparer.Ordinal);
    private bool closed;

    public WebView2Workspace(CoreWebView2 webView)
    {
        this.webView = webView ?? throw new ArgumentNullException(nameof(webView));
    }

    public string ProfileName => webView.Profile.ProfileName;
    public string ProfilePath => webView.Profile.ProfilePath;

    public IReadOnlyList<DocumentScriptInfo> DocumentScripts => scripts
        .Select(value => new DocumentScriptInfo(value.Key, value.Value.NativeId))
        .OrderBy(value => value.Id, StringComparer.Ordinal)
        .ToArray();

    /// <summary>
    /// Registers source prepared by res/web userscript tooling for future
    /// documents. This binding deliberately does not parse or widen matches.
    /// </summary>
    public async Task<DocumentScriptInfo> InstallDocumentScriptAsync(string id, string preparedSource)
    {
        ThrowIfClosed();
        ValidateScript(id, preparedSource);
        if (scripts.ContainsKey(id) || !pendingScripts.Add(id))
            throw new InvalidOperationException("Script ID is already installed");
        try
        {
            var nativeId = await webView.AddScriptToExecuteOnDocumentCreatedAsync(preparedSource);
            scripts.Add(id, new InstalledScript(preparedSource, nativeId));
            return new DocumentScriptInfo(id, nativeId);
        }
        finally
        {
            pendingScripts.Remove(id);
        }
    }

    public void RemoveDocumentScript(string id)
    {
        ThrowIfClosed();
        if (!scripts.TryGetValue(id, out var script))
            throw new KeyNotFoundException("Script ID is not installed");
        webView.RemoveScriptToExecuteOnDocumentCreated(script.NativeId);
        scripts.Remove(id);
    }

    /// <summary>Executes the same prepared source in the current page.</summary>
    public async Task<string> ExecuteDocumentScriptNowAsync(string id)
    {
        ThrowIfClosed();
        if (!scripts.TryGetValue(id, out var script))
            throw new KeyNotFoundException("Script ID is not installed");
        return await webView.ExecuteScriptAsync(script.Source);
    }

    /// <summary>
    /// Installs a caller-approved unpacked extension into this workspace's
    /// profile. The directory is revalidated immediately before dispatch.
    /// </summary>
    public async Task<BrowserExtensionInfo> InstallExtensionAsync(string unpackedDirectory)
    {
        ThrowIfClosed();
        var fullPath = ValidateExtensionDirectory(unpackedDirectory);
        var extension = await webView.Profile.AddBrowserExtensionAsync(fullPath);
        return Describe(extension);
    }

    public async Task<IReadOnlyList<BrowserExtensionInfo>> ListExtensionsAsync()
    {
        ThrowIfClosed();
        var installed = await webView.Profile.GetBrowserExtensionsAsync();
        return installed.Select(Describe)
            .OrderBy(value => value.Name, StringComparer.Ordinal)
            .ThenBy(value => value.Id, StringComparer.Ordinal)
            .ToArray();
    }

    public async Task SetExtensionEnabledAsync(string extensionId, bool enabled)
    {
        ThrowIfClosed();
        var extension = await FindExtensionAsync(extensionId);
        await extension.EnableAsync(enabled);
    }

    public async Task RemoveExtensionAsync(string extensionId)
    {
        ThrowIfClosed();
        var extension = await FindExtensionAsync(extensionId);
        await extension.RemoveAsync();
    }

    public ValueTask DisposeAsync()
    {
        if (closed) return ValueTask.CompletedTask;
        closed = true;
        foreach (var script in scripts.Values)
            webView.RemoveScriptToExecuteOnDocumentCreated(script.NativeId);
        scripts.Clear();
        pendingScripts.Clear();
        return ValueTask.CompletedTask;
    }

    private async Task<CoreWebView2BrowserExtension> FindExtensionAsync(string id)
    {
        if (string.IsNullOrWhiteSpace(id))
            throw new ArgumentException("Extension ID is required", nameof(id));
        var installed = await webView.Profile.GetBrowserExtensionsAsync();
        return installed.FirstOrDefault(item => item.Id == id)
            ?? throw new KeyNotFoundException("Extension is not installed in this WebView2 profile");
    }

    private static string ValidateExtensionDirectory(string path)
    {
        if (string.IsNullOrWhiteSpace(path) || !Path.IsPathFullyQualified(path))
            throw new ArgumentException("An absolute unpacked extension directory is required", nameof(path));
        var fullPath = Path.GetFullPath(path);
        var manifest = Path.Combine(fullPath, "manifest.json");
        if (!Directory.Exists(fullPath) || !File.Exists(manifest))
            throw new FileNotFoundException("Unpacked extension manifest.json was not found", manifest);
        return fullPath;
    }

    private static void ValidateScript(string id, string source)
    {
        if (string.IsNullOrWhiteSpace(id) || id.Length > 128 ||
            !id.All(value => char.IsAsciiLetterOrDigit(value) || value is '.' or '_' or '-'))
            throw new ArgumentException("Script ID is invalid", nameof(id));
        if (string.IsNullOrEmpty(source) || System.Text.Encoding.UTF8.GetByteCount(source) > 1_048_576)
            throw new ArgumentException("Prepared script source must contain 1 byte to 1 MiB", nameof(source));
    }

    private static BrowserExtensionInfo Describe(CoreWebView2BrowserExtension extension) =>
        new(extension.Id, extension.Name, extension.IsEnabled);

    private void ThrowIfClosed() => ObjectDisposedException.ThrowIf(closed, this);
}
