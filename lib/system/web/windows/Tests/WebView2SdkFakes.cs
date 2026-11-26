namespace Microsoft.Web.WebView2.Core;

public sealed class CoreWebView2
{
    public CoreWebView2Profile Profile { get; } = new();
    public Dictionary<string, string> Scripts { get; } = new();
    public string? LastExecutedScript { get; private set; }
    private int sequence;

    public Task<string> AddScriptToExecuteOnDocumentCreatedAsync(string source)
    {
        var id = (++sequence).ToString(System.Globalization.CultureInfo.InvariantCulture);
        Scripts[id] = source;
        return Task.FromResult(id);
    }

    public void RemoveScriptToExecuteOnDocumentCreated(string id) => Scripts.Remove(id);

    public Task<string> ExecuteScriptAsync(string source)
    {
        LastExecutedScript = source;
        return Task.FromResult("true");
    }
}

public sealed class CoreWebView2Profile
{
    public string ProfileName { get; set; } = "owner-profile";
    public string ProfilePath { get; set; } = "C:\\owner\\profile";
    public List<CoreWebView2BrowserExtension> Extensions { get; } = new();

    public Task<CoreWebView2BrowserExtension> AddBrowserExtensionAsync(string path)
    {
        var extension = new CoreWebView2BrowserExtension("test-id", "Test extension");
        Extensions.Add(extension);
        return Task.FromResult(extension);
    }

    public Task<IReadOnlyList<CoreWebView2BrowserExtension>> GetBrowserExtensionsAsync() =>
        Task.FromResult<IReadOnlyList<CoreWebView2BrowserExtension>>(
            Extensions.Where(item => !item.Removed).ToArray());
}

public sealed class CoreWebView2BrowserExtension
{
    public string Id { get; }
    public string Name { get; }
    public bool IsEnabled { get; private set; } = true;
    public bool Removed { get; private set; }

    public CoreWebView2BrowserExtension(string id, string name)
    {
        Id = id;
        Name = name;
    }

    public Task EnableAsync(bool enabled)
    {
        IsEnabled = enabled;
        return Task.CompletedTask;
    }

    public Task RemoveAsync()
    {
        Removed = true;
        return Task.CompletedTask;
    }
}
