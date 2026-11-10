// Ext.Clipboard.Windows on the real Win32 clipboard: the policy checks and the shared fixtures
// (Fixtures.cs), which need no clipboard, and round trips of text, a DIB and a registered format, which replace the clipboard's content
// and restore nothing. When this session has no clipboard (OpenClipboard keeps failing, as in a
// service session) the round trips say so and are skipped, or fail when
// EXT_CLIPBOARD_REQUIRED=1. Run it with
// dotnet run --project res/system/capture/clipboard/windows/Tests.
using System.Text;
using Webong.Ext.System.Windows;

var required = Environment.GetEnvironmentVariable("EXT_CLIPBOARD_REQUIRED") == "1";
var passed = 0;
var skipped = 0;
var failed = new List<string>();
void Test(string name, Action body)
{
    try
    {
        body();
        passed++;
        Console.WriteLine($"ok   {name}");
    }
    catch (Exception e)
    {
        failed.Add(name);
        Console.WriteLine($"FAIL {name}\n  {e}");
    }
}

static void Check(bool condition, string what)
{
    if (!condition) throw new Exception(what);
}

static ClipboardException Fails(ClipboardErrorKind kind, Action f, string what)
{
    try
    {
        f();
    }
    catch (ClipboardException e)
    {
        if (e.Kind != kind) throw new Exception($"{what}: {e.Kind} ({e.Message}), want {kind}");
        return e;
    }
    throw new Exception($"{what}: no ClipboardException, want {kind}");
}

const string Binary = "Ext.Clipboard.Windows.Tests.Binary";
ClipboardPolicy Policy(params string[] formats) =>
    new(formats, AllowRead: true, AllowWrite: true);
ClipboardItem Item(params (string Format, byte[] Data)[] representations) =>
    new(representations.Select(r => new ClipboardRepresentation(r.Format, r.Data)).ToArray());
byte[] Utf8(string text) => Encoding.UTF8.GetBytes(text);
ClipboardRepresentation Only(ClipboardSnapshot snapshot, string format)
{
    Check(snapshot.Items.Count == 1, $"{snapshot.Items.Count} items, want 1");
    var found = snapshot.Items[0].Representations.Where(r => r.Format == format).ToArray();
    Check(found.Length == 1, $"{found.Length} {format} representations, want 1");
    return found[0];
}

// ---- the policy and argument checks, which fail before the clipboard is opened ----

Test("a policy needs bounded formats and ceilings", () =>
{
    Fails(ClipboardErrorKind.Invalid, () => new ClipboardPolicy(Array.Empty<string>()).Validate(), "no formats");
    Fails(ClipboardErrorKind.Invalid, () => new ClipboardPolicy(new[] { "CF_TEXT" }, MaxBytes: 0).Validate(), "no bytes");
    Fails(ClipboardErrorKind.Invalid, () => new ClipboardPolicy(new[] { "CF_TEXT" }, MaxBytes: 16 * 1024 * 1024 + 1).Validate(), "too many bytes");
    Fails(ClipboardErrorKind.Invalid, () => new ClipboardPolicy(new[] { "CF_TEXT" }, MaxItems: 0).Validate(), "no items");
    Fails(ClipboardErrorKind.Invalid, () => new ClipboardPolicy(new[] { "CF_TEXT" }, MaxItems: 65).Validate(), "too many items");
    Fails(ClipboardErrorKind.Invalid, () => new ClipboardPolicy(new[] { "CF_TEXT" }, MaxFormats: 33).Validate(), "too many formats");
    Fails(ClipboardErrorKind.Invalid, () => new ClipboardPolicy(new[] { "CF_TEXT", "CF_DIB" }, MaxFormats: 1).Validate(), "more formats than the ceiling");
    Fails(ClipboardErrorKind.Invalid, () => new ClipboardPolicy(new[] { "CF_TEXT", "CF_TEXT" }).Validate(), "a repeated format");
    Fails(ClipboardErrorKind.Invalid, () => new ClipboardPolicy(new[] { "" }).Validate(), "an empty name");
    Fails(ClipboardErrorKind.Invalid, () => new ClipboardPolicy(new[] { new string('x', 257) }).Validate(), "a long name");
    var policy = new ClipboardPolicy(new[] { "CF_UNICODETEXT", new string('x', 256) });
    Check(ReferenceEquals(policy.Validate(), policy), "Validate returns the policy");
});

Test("read and write need permission", () =>
{
    Fails(ClipboardErrorKind.Denied, () => SystemClipboard.Read(new ClipboardPolicy(new[] { "CF_UNICODETEXT" })), "read");
    Fails(ClipboardErrorKind.Denied, () => SystemClipboard.Write(new ClipboardPolicy(new[] { "CF_UNICODETEXT" }, AllowRead: true),
        SystemClipboard.Generation(), new[] { Item(("CF_UNICODETEXT", Utf8("x"))) }), "write");
    Fails(ClipboardErrorKind.Denied, () => SystemClipboard.Clear(new ClipboardPolicy(new[] { "CF_UNICODETEXT" }),
        SystemClipboard.Generation()), "clear");
});

Test("a write is checked before the clipboard changes", () =>
{
    var policy = Policy("CF_UNICODETEXT", Binary);
    var generation = SystemClipboard.Generation();
    Fails(ClipboardErrorKind.Invalid, () => SystemClipboard.Write(policy, "", new[] { Item(("CF_UNICODETEXT", Utf8("x"))) }), "no generation");
    Fails(ClipboardErrorKind.Invalid, () => SystemClipboard.Write(policy, generation,
        new[] { Item(("CF_UNICODETEXT", Utf8("a"))), Item(("CF_UNICODETEXT", Utf8("b"))) }), "two items");
    Fails(ClipboardErrorKind.Invalid, () => SystemClipboard.Write(policy, generation, new[] { Item() }), "an item without formats");
    Fails(ClipboardErrorKind.Denied, () => SystemClipboard.Write(policy, generation, new[] { Item(("CF_TEXT", Utf8("x"))) }), "a format outside the policy");
    Fails(ClipboardErrorKind.Denied, () => SystemClipboard.Write(policy, generation,
        new[] { Item(("CF_UNICODETEXT", Utf8("a")), ("CF_UNICODETEXT", Utf8("b"))) }), "a repeated format");
    Fails(ClipboardErrorKind.Limit, () => SystemClipboard.Write(policy with { MaxBytes = 4 }, generation,
        new[] { Item(("CF_UNICODETEXT", Utf8("abc")), (Binary, new byte[] { 1, 2 })) }), "bytes over the ceiling");
    Fails(ClipboardErrorKind.Invalid, () => SystemClipboard.Write(policy with { MaxFormats = 1, AllowedFormats = new[] { Binary } }, generation,
        new[] { Item((Binary, new byte[] { 1 }), ("CF_UNICODETEXT", Utf8("x"))) }), "formats over the ceiling");
    Fails(ClipboardErrorKind.Changed, () => SystemClipboard.Write(policy, generation + "0",
        new[] { Item(("CF_UNICODETEXT", Utf8("x"))) }), "a stale generation");
    Check(SystemClipboard.Generation() == generation, "a refused write changed the clipboard");
});

Test("the generation names the Windows sequence number", () =>
{
    var generation = SystemClipboard.Generation();
    Check(generation.StartsWith("windows:", StringComparison.Ordinal) && uint.TryParse(generation["windows:".Length..], out _),
        $"generation {generation}");
});

// ---- the shared fixtures (res/system/testdata/clipboard/v1), which need no clipboard ----

Fixtures.Clipboard(Test);

// ---- round trips on the real clipboard ----

string? unavailable = null;
try
{
    // A refused read still opens the clipboard; only a clipboard that cannot be opened is missing.
    // The library has no capability report and calls this Denied, like a refused permission, so
    // only its text tells them apart (docs/adr-architecture.md, rules 2 and 3).
    SystemClipboard.Read(new ClipboardPolicy(new[] { Binary + ".Probe" }, AllowRead: true));
}
catch (ClipboardException e) when (e.Message.Contains("held by another process", StringComparison.Ordinal))
{
    unavailable = e.Message;
}
catch (ClipboardException)
{
}

void RoundTrip(string name, Action body)
{
    if (unavailable is null)
    {
        Test(name, body);
        return;
    }
    if (required)
    {
        failed.Add(name);
        Console.WriteLine($"FAIL {name}\n  no clipboard ({unavailable}) and EXT_CLIPBOARD_REQUIRED=1");
        return;
    }
    skipped++;
    Console.WriteLine($"skip {name}: no clipboard in this session ({unavailable})");
}

RoundTrip("Unicode text round trips as UTF-8", () =>
{
    var policy = Policy("CF_UNICODETEXT");
    var text = "ext ✓ héllo 𝄞 \r\n tab\t";
    var receipt = SystemClipboard.Write(policy, SystemClipboard.Generation(), new[] { Item(("CF_UNICODETEXT", Utf8(text))) });
    Check(receipt.Dispatch == "dispatched", $"dispatch {receipt.Dispatch}");
    Check(receipt.Generation == SystemClipboard.Generation(), "the receipt names the current generation");
    var snapshot = SystemClipboard.Read(policy);
    Check(snapshot.Generation == receipt.Generation, $"read generation {snapshot.Generation}, want {receipt.Generation}");
    var got = Encoding.UTF8.GetString(Only(snapshot, "CF_UNICODETEXT").Data);
    Check(got == text, $"read {got}, want {text}");
});

RoundTrip("a write moves the generation on", () =>
{
    var policy = Policy("CF_UNICODETEXT");
    var before = SystemClipboard.Generation();
    var receipt = SystemClipboard.Write(policy, before, new[] { Item(("CF_UNICODETEXT", Utf8("next"))) });
    Check(receipt.Generation != before, "the generation did not change");
    Fails(ClipboardErrorKind.Changed, () => SystemClipboard.Write(policy, before, new[] { Item(("CF_UNICODETEXT", Utf8("late"))) }), "a write with the old generation");
    Check(Encoding.UTF8.GetString(Only(SystemClipboard.Read(policy), "CF_UNICODETEXT").Data) == "next", "the late write replaced the text");
});

RoundTrip("Windows synthesizes CF_TEXT from Unicode text", () =>
{
    SystemClipboard.Write(Policy("CF_UNICODETEXT"), SystemClipboard.Generation(), new[] { Item(("CF_UNICODETEXT", Utf8("plain ascii"))) });
    var data = Only(SystemClipboard.Read(Policy("CF_TEXT")), "CF_TEXT").Data;
    var terminator = Array.IndexOf(data, (byte)0);
    Check(terminator >= 0, "CF_TEXT has no terminator");
    Check(Encoding.ASCII.GetString(data, 0, terminator) == "plain ascii", "CF_TEXT text");
});

RoundTrip("text that is not UTF-8 is refused", () =>
{
    Fails(ClipboardErrorKind.Invalid, () => SystemClipboard.Write(Policy("CF_UNICODETEXT"), SystemClipboard.Generation(),
        new[] { Item(("CF_UNICODETEXT", new byte[] { 0x66, 0xff, 0xfe })) }), "invalid UTF-8");
});

RoundTrip("a registered format round trips its bytes", () =>
{
    var policy = Policy(Binary);
    var payload = Enumerable.Range(0, 256).Select(i => (byte)i).ToArray();
    SystemClipboard.Write(policy, SystemClipboard.Generation(), new[] { Item((Binary, payload)) });
    var got = Only(SystemClipboard.Read(policy), Binary).Data;
    // GlobalSize may report more than was allocated: the payload must lead, nothing more is promised.
    Check(got.Length >= payload.Length && got.AsSpan(0, payload.Length).SequenceEqual(payload),
        $"read {got.Length} bytes that do not start with the payload");
});

RoundTrip("one item carries several formats", () =>
{
    var policy = Policy("CF_UNICODETEXT", Binary);
    SystemClipboard.Write(policy, SystemClipboard.Generation(),
        new[] { Item(("CF_UNICODETEXT", Utf8("both")), (Binary, new byte[] { 7, 8, 9 })) });
    var snapshot = SystemClipboard.Read(policy);
    Check(Encoding.UTF8.GetString(Only(snapshot, "CF_UNICODETEXT").Data) == "both", "the text");
    Check(Only(snapshot, Binary).Data.AsSpan(0, 3).SequenceEqual(new byte[] { 7, 8, 9 }), "the bytes");
    // Only the allowed formats are read, though Windows adds CF_TEXT and others for the text.
    Check(snapshot.Items[0].Representations.All(r => r.Format is "CF_UNICODETEXT" or Binary), "a format outside the policy was read");
});

RoundTrip("a DIB round trips and Windows offers it as CF_DIBV5", () =>
{
    // A 2x1 32-bit BITMAPINFOHEADER DIB: blue then red.
    var dib = new byte[40 + 8];
    BitConverter.GetBytes(40).CopyTo(dib, 0);        // biSize
    BitConverter.GetBytes(2).CopyTo(dib, 4);         // biWidth
    BitConverter.GetBytes(1).CopyTo(dib, 8);         // biHeight
    BitConverter.GetBytes((short)1).CopyTo(dib, 12); // biPlanes
    BitConverter.GetBytes((short)32).CopyTo(dib, 14); // biBitCount
    BitConverter.GetBytes(8).CopyTo(dib, 20);        // biSizeImage
    new byte[] { 0xff, 0, 0, 0, 0, 0, 0xff, 0 }.CopyTo(dib, 40);
    SystemClipboard.Write(Policy("CF_DIB"), SystemClipboard.Generation(), new[] { Item(("CF_DIB", dib)) });
    var got = Only(SystemClipboard.Read(Policy("CF_DIB")), "CF_DIB").Data;
    Check(got.Length >= dib.Length && got.AsSpan(0, dib.Length).SequenceEqual(dib), "the DIB changed");
    var v5 = Only(SystemClipboard.Read(Policy("CF_DIBV5")), "CF_DIBV5").Data;
    Check(v5.Length >= 124 && BitConverter.ToInt32(v5, 0) == 124, "CF_DIBV5 has no BITMAPV5HEADER");
    Check(BitConverter.ToInt32(v5, 4) == 2 && BitConverter.ToInt32(v5, 8) == 1, "CF_DIBV5 size");
});

RoundTrip("an empty representation is refused", () =>
{
    Fails(ClipboardErrorKind.Invalid, () => SystemClipboard.Write(Policy(Binary), SystemClipboard.Generation(),
        new[] { Item((Binary, Array.Empty<byte>())) }), "empty data");
});

RoundTrip("a read is bounded by the policy", () =>
{
    SystemClipboard.Write(Policy(Binary), SystemClipboard.Generation(), new[] { Item((Binary, new byte[100])) });
    Fails(ClipboardErrorKind.Limit, () => SystemClipboard.Read(Policy(Binary) with { MaxBytes = 10 }), "a small ceiling");
    Fails(ClipboardErrorKind.Denied, () => SystemClipboard.Read(Policy("CF_UNICODETEXT")), "none of the allowed formats");
});

RoundTrip("clear empties the clipboard", () =>
{
    var policy = Policy("CF_UNICODETEXT");
    SystemClipboard.Write(policy, SystemClipboard.Generation(), new[] { Item(("CF_UNICODETEXT", Utf8("gone soon"))) });
    var receipt = SystemClipboard.Clear(policy, SystemClipboard.Generation());
    Check(receipt.Generation == SystemClipboard.Generation(), "the receipt names the current generation");
    Fails(ClipboardErrorKind.Denied, () => SystemClipboard.Read(policy), "a read of a cleared clipboard");
});

Console.WriteLine($"{passed} passed, {skipped} skipped, {failed.Count} failed");
foreach (var name in failed) Console.WriteLine($"  {name}");
return failed.Count == 0 ? 0 : 1;
