using System.Buffers.Binary;
using System.IO.Compression;

namespace Webong.Ext.System.Windows;

// A bounded RGB PNG encoder for the native BGRx DIB. It streams rows and IDAT chunks.
internal static class SurfacePng
{
    private static readonly uint[] CrcTable = BuildCrcTable();

    internal static byte[] Encode(int width, int height, int maxBytes, Action<int, byte[]> readRow, CancellationToken token)
    {
        using var output = new MemoryStream();
        using var chunks = new PngChunks(output, maxBytes);
        chunks.WriteSignature();
        Span<byte> header = stackalloc byte[13];
        header.Clear();
        BinaryPrimitives.WriteInt32BigEndian(header, width);
        BinaryPrimitives.WriteInt32BigEndian(header[4..], height);
        header[8] = 8;
        header[9] = 2; // RGB
        chunks.WriteChunk("IHDR"u8, header);
        using (var compressed = new ZLibStream(chunks, CompressionLevel.Fastest, leaveOpen: true))
        {
            var source = new byte[checked(width * 4)];
            var row = new byte[checked(1 + width * 3)]; // filter zero
            for (int y = 0; y < height; y++)
            {
                token.ThrowIfCancellationRequested();
                readRow(y, source);
                for (int x = 0, from = 0, to = 1; x < width; x++, from += 4, to += 3)
                {
                    row[to] = source[from + 2];
                    row[to + 1] = source[from + 1];
                    row[to + 2] = source[from];
                }
                compressed.Write(row);
            }
        }
        chunks.WriteChunk("IEND"u8, []);
        return output.ToArray();
    }

    private static uint[] BuildCrcTable()
    {
        var table = new uint[256];
        for (uint i = 0; i < table.Length; i++)
        {
            uint crc = i;
            for (int bit = 0; bit < 8; bit++) crc = (crc >> 1) ^ ((crc & 1) == 0 ? 0 : 0xedb88320);
            table[i] = crc;
        }
        return table;
    }

    private static uint UpdateCrc(uint crc, ReadOnlySpan<byte> payload)
    {
        foreach (var value in payload) crc = CrcTable[(crc ^ value) & 255] ^ (crc >> 8);
        return crc;
    }

    private sealed class PngChunks(MemoryStream output, int maxBytes) : Stream
    {
        private void CheckLength(long more)
        {
            if (output.Length + more > maxBytes)
                throw WindowSurface.Failure(SurfaceError.LimitExceeded, "The PNG exceeds the capture byte limit.");
        }

        internal void WriteSignature()
        {
            CheckLength(8);
            output.Write([137, 80, 78, 71, 13, 10, 26, 10]);
        }

        internal void WriteChunk(ReadOnlySpan<byte> type, ReadOnlySpan<byte> payload)
        {
            CheckLength(12L + payload.Length);
            Span<byte> number = stackalloc byte[4];
            BinaryPrimitives.WriteInt32BigEndian(number, payload.Length);
            output.Write(number);
            output.Write(type);
            output.Write(payload);
            uint crc = UpdateCrc(UpdateCrc(uint.MaxValue, type), payload);
            BinaryPrimitives.WriteUInt32BigEndian(number, ~crc);
            output.Write(number);
        }

        public override void Write(ReadOnlySpan<byte> buffer) => WriteChunk("IDAT"u8, buffer);
        public override void Write(byte[] buffer, int offset, int count) => Write(buffer.AsSpan(offset, count));
        public override bool CanWrite => true;
        public override bool CanRead => false;
        public override bool CanSeek => false;
        public override void Flush() { }
        public override long Length => throw new NotSupportedException();
        public override long Position { get => throw new NotSupportedException(); set => throw new NotSupportedException(); }
        public override long Seek(long offset, SeekOrigin origin) => throw new NotSupportedException();
        public override void SetLength(long value) => throw new NotSupportedException();
        public override int Read(byte[] buffer, int offset, int count) => throw new NotSupportedException();
    }
}
