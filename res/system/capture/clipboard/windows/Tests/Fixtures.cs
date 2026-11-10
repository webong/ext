// The shared clipboard fixtures (res/system/testdata/clipboard/v1), which the Go, Swift and Java
// bindings run as well: the policy section through ClipboardPolicy.Validate and the items section
// through ClipboardValidation.ValidateItems, each case's code against ClipboardException.Code.
// The testdata is EXT_SYSTEM_TESTDATA when set, else the res/system/testdata found by walking up.
using System.Text;
using System.Text.Json.Nodes;
using Webong.Ext.System.Windows;

static class Fixtures
{
    public static string Dir()
    {
        var overridden = Environment.GetEnvironmentVariable("EXT_SYSTEM_TESTDATA");
        if (!string.IsNullOrEmpty(overridden)) return overridden;
        for (var dir = new DirectoryInfo(AppContext.BaseDirectory); dir is not null; dir = dir.Parent)
        {
            var candidate = Path.Combine(dir.FullName, "res", "system", "testdata");
            if (File.Exists(Path.Combine(candidate, "codes.json"))) return candidate;
        }
        throw new Exception("res/system/testdata was not found above " + AppContext.BaseDirectory +
            ": run from a checkout or set EXT_SYSTEM_TESTDATA");
    }

    static JsonNode Load(string dir, params string[] path) =>
        JsonNode.Parse(File.ReadAllText(Path.Combine(new[] { dir }.Concat(path).ToArray())))!;

    /// <summary>Adds one test per case, named policy/&lt;name&gt; and items/&lt;name&gt;.</summary>
    public static void Clipboard(Action<string, Action> test)
    {
        var dir = Dir();
        var codes = Load(dir, "codes.json")["codes"]!.AsArray().Select(c => (string)c!).ToHashSet(StringComparer.Ordinal);
        var cases = Load(dir, "clipboard", "v1", "cases.json");
        if ((int)cases["version"]! != 1) throw new Exception("clipboard/v1/cases.json is not version 1");
        var limits = cases["limits"]!;
        var maxItems = (int)limits["maxItems"]!;
        var maxFormats = (int)limits["maxFormats"]!;
        var policies = cases["policy"]!.AsArray();
        var itemCases = cases["items"]!.AsArray();
        if (policies.Count == 0 || itemCases.Count == 0) throw new Exception("clipboard/v1 has no cases");

        foreach (var c in policies)
        {
            var name = (string)c!["name"]!;
            test("fixture policy/" + name, () =>
            {
                var formats = Formats(c);
                Expect(codes, c, () => new ClipboardPolicy(formats, MaxBytes: (int)c["maxBytes"]!).Validate());
            });
        }
        foreach (var c in itemCases)
        {
            var name = (string)c!["name"]!;
            test("fixture items/" + name, () =>
            {
                var formats = Formats(c);
                var items = c["items"]!.AsArray().SelectMany(Build).ToArray();
                Expect(codes, c, () => ClipboardValidation.ValidateItems(items, (int)c["maxBytes"]!, maxItems, maxFormats, formats));
            });
        }
    }

    static string[] Formats(JsonNode c) => c["formats"]!.AsArray().Select(f => (string)f!).ToArray();

    // A representation is text (its UTF-8 bytes) or zeros (that many zero bytes); an item with
    // times is repeated.
    static IEnumerable<ClipboardItem> Build(JsonNode? item)
    {
        var times = item!["times"] is JsonNode t ? (int)t : 1;
        var representations = item["representations"]!.AsArray().Select(r =>
            new ClipboardRepresentation((string)r!["format"]!,
                r["text"] is JsonNode text ? Encoding.UTF8.GetBytes((string)text!) : new byte[r["zeros"] is JsonNode z ? (int)z : 0]))
            .ToArray();
        return Enumerable.Range(0, times).Select(_ => new ClipboardItem(representations));
    }

    static void Expect(HashSet<string> codes, JsonNode c, Action run)
    {
        var want = c["code"]?.GetValue<string>();
        if (want is not null && !codes.Contains(want)) throw new Exception($"code {want} is not in testdata/codes.json");
        try
        {
            run();
        }
        catch (ClipboardException e)
        {
            if (want is null) throw new Exception($"rejected as {e.Code} ({e.Message}), want accepted");
            if (e.Code != want) throw new Exception($"code {e.Code} ({e.Message}), want {want}");
            return;
        }
        if (want is not null) throw new Exception($"accepted, want {want}");
    }
}
