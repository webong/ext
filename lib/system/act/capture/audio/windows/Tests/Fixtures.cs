// The shared audio fixtures (res/system/testdata/audio/v1): the playback section, the packet rule the
// Go, Swift and Java playback sinks share, through SystemAudioPlayback.CheckPacket (what Write runs),
// each case's code against AudioPacketException.Code, and capability/v1 through the AudioStatus
// constructor, the capability report. The other sections are Go's (the README of
// audio/v1). The testdata is EXT_SYSTEM_TESTDATA when set, else the res/system/testdata found by
// walking up.
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

    public static HashSet<string> Codes() =>
        Load(Dir(), "codes.json")["codes"]!.AsArray().Select(c => (string)c!).ToHashSet(StringComparer.Ordinal);

    /// <summary>
    /// Adds one test per capability/v1 case, named fixture capability/&lt;state&gt;+&lt;reason&gt;: a
    /// valid pair makes an AudioStatus, an invalid one is refused with the case's code.
    /// </summary>
    public static void Capability(Action<string, Action> test)
    {
        var dir = Dir();
        var codes = Codes();
        var cases = Load(dir, "capability", "v1", "cases.json");
        if ((int)cases["version"]! != 1) throw new Exception("capability/v1/cases.json is not version 1");
        var all = cases["cases"]!.AsArray();
        if (all.Count == 0) throw new Exception("capability/v1 has no cases");
        foreach (var c in all)
        {
            var state = (string)c!["state"]!;
            var reason = c["reason"]?.GetValue<string>();
            test($"fixture capability/{state}+{reason ?? "none"}", () =>
                Expect(codes, c, () =>
                {
                    var status = new AudioStatus(state, reason);
                    if (status.State != state || status.Reason != reason) throw new Exception($"status {status}");
                }));
        }
    }

    /// <summary>Adds one test per playback case, named fixture playback/&lt;name&gt;.</summary>
    public static void Playback(Action<string, Action> test)
    {
        var dir = Dir();
        var codes = Codes();
        var cases = Load(dir, "audio", "v1", "cases.json");
        if ((int)cases["version"]! != 1) throw new Exception("audio/v1/cases.json is not version 1");
        var defaults = cases["defaults"]!;
        if ((int)defaults["playbackPacketBytes"]! != SystemAudioPlayback.MaxPacketBytes)
            throw new Exception($"the fixtures' playback ceiling is {defaults["playbackPacketBytes"]}, Write's is {SystemAudioPlayback.MaxPacketBytes}");
        var playback = cases["playback"]!.AsArray();
        if (playback.Count == 0) throw new Exception("audio/v1 has no playback cases");
        foreach (var c in playback)
        {
            var name = (string)c!["name"]!;
            test("fixture playback/" + name, () =>
                Expect(codes, c, () => SystemAudioPlayback.CheckPacket((int)c["bytes"]!, (int)c["maxBytes"]!)));
        }
    }

    static void Expect(HashSet<string> codes, JsonNode c, Action run)
    {
        var want = c["code"]?.GetValue<string>();
        if (want is not null && !codes.Contains(want)) throw new Exception($"code {want} is not in testdata/codes.json");
        try
        {
            run();
        }
        catch (AudioArgumentException e)
        {
            if (want is null) throw new Exception($"rejected as {e.Code} ({e.Message}), want accepted");
            if (e.Code != want) throw new Exception($"code {e.Code} ({e.Message}), want {want}");
            return;
        }
        if (want is not null) throw new Exception($"accepted, want {want}");
    }
}
