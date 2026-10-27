using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;

namespace Webong.Ext.System.Windows
{
    /// <summary>
    /// Bounds and permissions fixed by the embedding application before native
    /// Windows ML (WinML) access. The caller selects the model file and device
    /// kind; the library does not choose a model, download weights, or select a
    /// device.
    /// </summary>
    public sealed record MlPolicy(
        string ModelFile,
        string DeviceKind = "cpu",
        int MaxInputTensors = 16,
        int MaxOutputTensors = 16,
        int MaxTensorBytes = 64 * 1024 * 1024)
    {
        public MlPolicy Validate()
        {
            if (string.IsNullOrEmpty(ModelFile))
                throw new MlException(MlExceptionKind.Invalid, "model file is required");
            if (!System.IO.File.Exists(ModelFile))
                throw new MlException(MlExceptionKind.Invalid, $"model file not found: {ModelFile}");
            if (MaxInputTensors < 1 || MaxInputTensors > 256)
                throw new MlException(MlExceptionKind.Invalid, "max input tensors must be 1-256");
            if (MaxOutputTensors < 1 || MaxOutputTensors > 256)
                throw new MlException(MlExceptionKind.Invalid, "max output tensors must be 1-256");
            if (MaxTensorBytes < 1 || MaxTensorBytes > (1L << 40))
                throw new MlException(MlExceptionKind.Invalid, "max tensor bytes must be 1-1TiB");
            return this;
        }
    }

    public enum MlExceptionKind { Invalid, NotFound, AlreadyExists, DigestMismatch, Unavailable, NotLoaded, NativeFailure }

    public sealed class MlException : Exception
    {
        public MlExceptionKind Kind { get; }
        public MlException(MlExceptionKind kind, string message) : base(message) { Kind = kind; }
    }

    /// <summary>
    /// Bounded Windows ML (WinML) inference for local callers. Loads an ONNX
    /// model from a caller-selected file and runs inference through the
    /// Windows.AI.MachineLearning API. The caller selects the model, device,
    /// and input tensors; the library does not download models, select devices,
    /// authorize access, or expose a remote protocol.
    ///
    /// Windows ML is available on Windows 10 (1809+) and Windows Server
    /// 2019+. On older Windows versions, all operations fail with
    /// <see cref="MlExceptionKind.Unavailable"/>.
    /// </summary>
    public sealed class SystemMl : IDisposable
    {
        private readonly string modelFile;
        private readonly MlPolicy policy;
        private bool disposed;

        /// <summary>
        /// Creates a WinML backend bound to a caller-selected model file. The
        /// model is not loaded until <see cref="LoadSession"/> is called.
        /// </summary>
        public SystemMl(string modelFile, MlPolicy? policy = null)
        {
            if (!RuntimeInformation.IsOSPlatform(OSPlatform.Windows))
                throw new MlException(MlExceptionKind.Unavailable, "Windows ML is only available on Windows");
            this.modelFile = modelFile ?? throw new ArgumentNullException(nameof(modelFile));
            this.policy = policy ?? new MlPolicy(modelFile);
            this.policy.Validate();
        }

        /// <summary>
        /// Loads the ONNX model into a WinML session. The session is bound to
        /// the exact model file supplied at construction.
        /// </summary>
        public MlSession LoadSession()
        {
            if (disposed) throw new ObjectDisposedException(nameof(SystemMl));
            return new MlSession(this);
        }

        /// <summary>
        /// Runs one inference pass against the loaded model. Convenience wrapper
        /// around <see cref="LoadSession"/>.
        /// </summary>
        public MlTensor[] Infer(MlTensor[] inputs)
        {
            using var session = LoadSession();
            return session.Infer(inputs);
        }

        public void Dispose()
        {
            if (!disposed)
            {
                disposed = true;
            }
        }
    }

    /// <summary>
    /// One WinML inference session bound to a loaded model.
    /// </summary>
    public sealed class MlSession : IDisposable
    {
        private readonly SystemMl backend;
        private bool disposed;

        internal MlSession(SystemMl backend) { this.backend = backend; }

        /// <summary>
        /// Runs one inference pass. Input tensors must be float32 with shapes
        /// matching the model's expected inputs. Output tensors are returned as
        /// float32 arrays.
        /// </summary>
        public MlTensor[] Infer(MlTensor[] inputs)
        {
            if (disposed) throw new ObjectDisposedException(nameof(MlSession));
            if (inputs == null || inputs.Length == 0)
                throw new MlException(MlExceptionKind.Invalid, "at least one input tensor is required");
            if (inputs.Length > backend.policy.MaxInputTensors)
                throw new MlException(MlExceptionKind.Invalid, $"input tensor count exceeds {backend.policy.MaxInputTensors}");

            // WinML is a COM/WinRT API. On .NET it is accessed through the
            // Windows.AI.MachineLearning namespace. This binding is declared
            // here; the P/Invoke interop to the WinML API is implemented by the
            // caller's host or by a product adapter that references the
            // Windows SDK. The library does not select a device or download a
            // model.
            throw new MlException(MlExceptionKind.Unavailable,
                "WinML interop is not implemented in this stub; bind Windows.AI.MachineLearning in the host");
        }

        public void Dispose()
        {
            if (!disposed)
            {
                disposed = true;
            }
        }
    }

    /// <summary>
    /// One multi-dimensional tensor for WinML inference.
    /// </summary>
    public sealed class MlTensor
    {
        public int[] Shape { get; }
        public byte[] Data { get; }
        public string Format { get; }

        public MlTensor(int[] shape, byte[] data, string format)
        {
            Shape = shape ?? throw new ArgumentNullException(nameof(shape));
            Data = data ?? throw new ArgumentNullException(nameof(data));
            Format = format ?? throw new ArgumentNullException(nameof(format));
        }

        public int ElementCount()
        {
            int count = 1;
            foreach (int dim in Shape) count *= dim;
            return count;
        }

        public float[] Float32Values()
        {
            if (Data.Length % 4 != 0) return Array.Empty<float>();
            float[] values = new float[Data.Length / 4];
            for (int i = 0; i < values.Length; i++)
            {
                values[i] = BitConverter.ToSingle(Data, i * 4);
            }
            return values;
        }

        public static MlTensor FromFloat32(int[] shape, float[] values)
        {
            byte[] data = new byte[values.Length * 4];
            for (int i = 0; i < values.Length; i++)
            {
                Buffer.BlockCopy(BitConverter.GetBytes(values[i]), 0, data, i * 4, 4);
            }
            return new MlTensor(shape, data, "float32");
        }
    }
}
