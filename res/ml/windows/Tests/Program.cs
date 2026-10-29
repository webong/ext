using System.Text;
using Webong.Ext.ML.Windows;

// A tiny ONNX Identity graph generated here keeps the native test independent
// of a downloaded model or a heavyweight model-authoring toolchain.
static byte[] Varint(ulong value)
{
    var bytes = new List<byte>();
    do {
        byte next = (byte)(value & 0x7f);
        value >>= 7;
        bytes.Add(value == 0 ? next : (byte)(next | 0x80));
    } while (value != 0);
    return bytes.ToArray();
}
static byte[] Number(int field, ulong value) => Varint((ulong)(field << 3)).Concat(Varint(value)).ToArray();
static byte[] Bytes(int field, byte[] value) => Varint((ulong)((field << 3) | 2)).Concat(Varint((ulong)value.Length)).Concat(value).ToArray();
static byte[] Text(int field, string value) => Bytes(field, Encoding.UTF8.GetBytes(value));
static byte[] Join(params byte[][] chunks) => chunks.SelectMany(bytes => bytes).ToArray();
static byte[] ValueInfo(string name)
{
    var shape = Join(Bytes(1, Number(1, 1)), Bytes(1, Number(1, 2)));
    var tensor = Join(Number(1, 1), Bytes(2, shape)); // FLOAT, shape [1,2]
    return Join(Text(1, name), Bytes(2, Bytes(1, tensor)));
}
static byte[] IdentityModel()
{
    var node = Join(Text(1, "x"), Text(2, "y"), Text(4, "Identity"));
    var graph = Join(Bytes(1, node), Text(2, "identity"), Bytes(11, ValueInfo("x")), Bytes(12, ValueInfo("y")));
    return Join(Number(1, 8), Text(2, "ext-test"), Bytes(7, graph), Bytes(8, Number(2, 13)));
}

var path = Path.Combine(Path.GetTempPath(), "ext-ml-identity-" + Guid.NewGuid().ToString("N") + ".onnx");
try
{
    File.WriteAllBytes(path, IdentityModel());
    using var ml = new SystemMl(path);
    var outputs = ml.Infer(new[] { MlTensor.FromFloat32(new[] { 1, 2 }, new[] { 2f, 3f }) });
    if (outputs.Length != 1 || outputs[0].Float32Values() is not [2f, 3f])
        throw new Exception("ONNX Identity inference returned the wrong tensor");
    using var bounded = new SystemMl(path, new MlPolicy(path, MaxTensorBytes: 4));
    try {
        bounded.Infer(new[] { MlTensor.FromFloat32(new[] { 1, 2 }, new[] { 2f, 3f }) });
        throw new Exception("input bound was not enforced");
    } catch (MlException e) when (e.Kind == MlExceptionKind.Invalid) { }
    Console.WriteLine("Windows ONNX inference passed");
}
finally { File.Delete(path); }
