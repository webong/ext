using System.Security.Cryptography;
using System.Text.Json;
using System.Text.RegularExpressions;
using Webong.Ext.ML.Windows;

// Package the published single-file executable as ID onnxruntime, version 1.0.0.
// Its descriptor revision is the SHA-256 of those exact executable bytes.
const string ApiVersion = "ext.plugin/v1";
const int MaxFrameBytes = 24 << 20;
const int MaxTensorBytes = 16 << 20;
var jsonOptions = new JsonSerializerOptions { PropertyNamingPolicy = JsonNamingPolicy.CamelCase,
    PropertyNameCaseInsensitive = true };

if (args.Length != 1 || args[0] != "run" || Environment.ProcessPath is not { } binary)
    return 2;

string revision;
using (var executable = File.OpenRead(binary))
    revision = Convert.ToHexString(SHA256.HashData(executable)).ToLowerInvariant();
var identity = new { id = "onnxruntime", revision, version = "1.0.0" };
var descriptor = new { apiVersion = ApiVersion, identity,
    contracts = new[] { new { name = "model.tensor", version = "v1",
        operations = new[] { new { name = "infer", surface = "action" } } } } };

try
{
    var input = Console.OpenStandardInput();
    var output = Console.OpenStandardOutput();
    var pending = new MemoryStream();
    var chunk = new byte[64 << 10];
    var handshaken = false;
    int count;
    while ((count = input.Read(chunk)) > 0)
    {
        var start = 0;
        for (var i = 0; i < count; i++)
        {
            if (chunk[i] != '\n') continue;
            pending.Write(chunk, start, i - start);
            if (pending.Length > MaxFrameBytes) throw new InvalidDataException("frame too large");
            using var document = JsonDocument.Parse(pending.ToArray());
            var request = document.RootElement;
            var id = request.GetProperty("id").GetString() ?? throw new InvalidDataException("missing ID");
            if (request.GetProperty("apiVersion").GetString() != ApiVersion ||
                request.GetProperty("deadline").GetDateTimeOffset() <= DateTimeOffset.UtcNow)
                throw new InvalidDataException("invalid request envelope");
            if (!handshaken)
            {
                if (id != "hello" || request.GetProperty("operation").GetString() != "plugin.hello")
                    throw new InvalidDataException("invalid handshake");
                Write(output, new { apiVersion = ApiVersion, id, payload = descriptor });
                handshaken = true;
            }
            else
            {
                if (request.GetProperty("operation").GetString() != "infer" ||
                    request.GetProperty("surface").GetString() != "action" ||
                    request.GetProperty("plugin").GetProperty("id").GetString() != identity.id ||
                    request.GetProperty("plugin").GetProperty("revision").GetString() != identity.revision ||
                    request.GetProperty("plugin").GetProperty("version").GetString() != identity.version ||
                    request.GetProperty("contract").GetProperty("name").GetString() != "model.tensor" ||
                    request.GetProperty("contract").GetProperty("version").GetString() != "v1")
                {
                    Write(output, new { apiVersion = ApiVersion, id,
                        error = new { code = "invalid_input", message = "request does not match model.tensor/v1" } });
                }
                else
                {
                    try
                    {
                        var body = request.GetProperty("payload").Deserialize<InferRequest>(jsonOptions)
                            ?? throw new InvalidDataException("missing payload");
                        var tensors = Infer(body);
                        Write(output, new { apiVersion = ApiVersion, id, payload = new { outputs = tensors } });
                    }
                    catch (Exception error) when (error is InvalidDataException or JsonException or ArgumentException)
                    {
                        Write(output, new { apiVersion = ApiVersion, id,
                            error = new { code = "invalid_input", message = "invalid tensor inference request" } });
                    }
                    catch (Exception)
                    {
                        Write(output, new { apiVersion = ApiVersion, id,
                            error = new { code = "native_failure", message = "ONNX model inference failed" } });
                    }
                }
            }
            pending.SetLength(0);
            start = i + 1;
        }
        pending.Write(chunk, start, count - start);
        if (pending.Length > MaxFrameBytes) throw new InvalidDataException("frame too large");
    }
    return pending.Length == 0 ? 0 : 1;
}
catch (Exception error)
{
    Console.Error.WriteLine($"ext-onnx-guest: {error.GetType().Name}");
    return 1;
}

void Write(Stream output, object frame)
{
    var data = JsonSerializer.SerializeToUtf8Bytes(frame, jsonOptions);
    if (data.Length > MaxFrameBytes) throw new InvalidDataException("frame too large");
    output.Write(data);
    output.WriteByte((byte)'\n');
    output.Flush();
}

TensorWire[] Infer(InferRequest body)
{
    if (body.Root is null || !Path.IsPathFullyQualified(body.Root) ||
        body.Reference is null || body.Inputs is null || body.Inputs.Length is < 1 or > 64)
        throw new InvalidDataException("invalid inference request");
    var slash = body.Reference.IndexOf('/');
    var at = body.Reference.IndexOf('@');
    if (slash != "onnxruntime".Length || at <= slash + 1 || at == body.Reference.Length - 1 ||
        body.Reference.IndexOf('/', slash + 1) >= 0 || body.Reference.IndexOf('@', at + 1) >= 0)
        throw new InvalidDataException("invalid model reference");
    var provider = body.Reference[..slash];
    var name = body.Reference[(slash + 1)..at];
    var version = body.Reference[(at + 1)..];
    if (provider != "onnxruntime" || !SafePart(name) || !SafePart(version))
        throw new InvalidDataException("invalid model reference");
    var total = 0;
    var inputs = new MlTensor[body.Inputs.Length];
    for (var i = 0; i < body.Inputs.Length; i++)
    {
        var tensor = body.Inputs[i];
        if (tensor.Shape is null || tensor.Data is null || tensor.Format != "float32" ||
            tensor.Data.Length > MaxTensorBytes - total)
            throw new InvalidDataException("invalid input tensor");
        total += tensor.Data.Length;
        inputs[i] = new MlTensor(tensor.Shape, tensor.Data, tensor.Format);
    }
    var modelDirectory = Path.Combine(body.Root, provider, name, version);
    foreach (var directory in new[] { body.Root, Path.Combine(body.Root, provider),
        Path.Combine(body.Root, provider, name), modelDirectory })
    {
        if ((new DirectoryInfo(directory).Attributes & FileAttributes.ReparsePoint) != 0)
            throw new InvalidDataException("linked model directory");
    }
    var manifestPath = Path.Combine(modelDirectory, "manifest.json");
    if (new FileInfo(manifestPath).Length > 64 << 10 ||
        (File.GetAttributes(manifestPath) & FileAttributes.ReparsePoint) != 0)
        throw new InvalidDataException("invalid model manifest");
    using var manifest = JsonDocument.Parse(File.ReadAllBytes(manifestPath));
    var meta = manifest.RootElement;
    if (meta.GetProperty("apiVersion").GetString() != "content.model/v1" ||
        meta.GetProperty("provider").GetString() != provider ||
        meta.GetProperty("name").GetString() != name ||
        meta.GetProperty("version").GetString() != version)
        throw new InvalidDataException("model identity changed");
    string? modelFile = null;
    foreach (var artifact in meta.GetProperty("artifacts").EnumerateArray())
    {
        var fileName = artifact.GetProperty("file").GetString();
        var digest = artifact.GetProperty("sha256").GetString();
        if (fileName is null || !SafePart(fileName) || digest is null || digest.Length != 64)
            throw new InvalidDataException("invalid model artifact");
        var path = Path.Combine(modelDirectory, fileName);
        if ((File.GetAttributes(path) & FileAttributes.ReparsePoint) != 0)
            throw new InvalidDataException("linked model artifact");
        using var file = File.OpenRead(path);
        var actual = Convert.ToHexString(SHA256.HashData(file)).ToLowerInvariant();
        if (actual != digest) throw new InvalidDataException("model artifact changed");
        if (artifact.GetProperty("role").GetString() == "model")
        {
            if (modelFile is not null || !fileName.EndsWith(".onnx", StringComparison.Ordinal))
                throw new InvalidDataException("expected one ONNX model");
            modelFile = path;
        }
    }
    if (modelFile is null) throw new InvalidDataException("missing ONNX model");
    using var backend = new SystemMl(modelFile, new MlPolicy(modelFile,
        MaxInputTensors: 64, MaxOutputTensors: 64, MaxTensorBytes: MaxTensorBytes));
    var outputs = backend.Infer(inputs);
    if (outputs.Length is < 1 or > 64) throw new InvalidDataException("invalid output count");
    total = 0;
    var wire = new TensorWire[outputs.Length];
    for (var i = 0; i < outputs.Length; i++)
    {
        var tensor = outputs[i];
        if (tensor.Data.Length > MaxTensorBytes - total)
            throw new InvalidDataException("output tensors exceed wire limit");
        total += tensor.Data.Length;
        wire[i] = new TensorWire(tensor.Shape, tensor.Data, tensor.Format);
    }
    return wire;
}

static bool SafePart(string value) => value.Length is > 0 and <= 128 &&
    Regex.IsMatch(value, @"^[A-Za-z0-9][A-Za-z0-9._-]*$") && value is not "." and not "..";

sealed record InferRequest(string Root, string Reference, TensorWire[] Inputs);
sealed record TensorWire(int[] Shape, byte[] Data, string Format);
