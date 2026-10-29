using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Runtime.InteropServices;
using Microsoft.ML.OnnxRuntime;

namespace Webong.Ext.ML.Windows
{
    /// <summary>
    /// Bounds and permissions fixed by the embedding application before native
    /// ONNX Runtime access. The caller selects the model file and device
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
            if (!File.Exists(ModelFile))
                throw new MlException(MlExceptionKind.Invalid, $"model file not found: {ModelFile}");
            if (MaxInputTensors < 1 || MaxInputTensors > 256)
                throw new MlException(MlExceptionKind.Invalid, "max input tensors must be 1-256");
            if (MaxOutputTensors < 1 || MaxOutputTensors > 256)
                throw new MlException(MlExceptionKind.Invalid, "max output tensors must be 1-256");
            if (MaxTensorBytes < 1)
                throw new MlException(MlExceptionKind.Invalid, "max tensor bytes must be positive");
            return this;
        }
    }

    public enum MlExceptionKind { Invalid, NotFound, AlreadyExists, DigestMismatch, Unavailable, NotLoaded, NativeFailure }

    public sealed class MlException : Exception
    {
        public MlExceptionKind Kind { get; }
        public MlException(MlExceptionKind kind, string message) : base(message) { Kind = kind; }
        public MlException(MlExceptionKind kind, string message, Exception cause) : base(message, cause) { Kind = kind; }
    }

    /// <summary>
    /// Bounded ONNX inference for local Windows and Linux callers. The caller selects the model, device,
    /// and input tensors; the library does not download models, select devices,
    /// authorize access, or expose a remote protocol.
    ///
    /// This target uses the optional Microsoft.ML.OnnxRuntime CPU package.
    /// </summary>
    public sealed class SystemMl : IDisposable
    {
        private readonly string modelFile;
        internal readonly MlPolicy policy;
        private bool disposed;

        /// <summary>
        /// Creates an ONNX backend bound to a caller-selected model file. The
        /// model is not loaded until <see cref="LoadSession"/> is called.
        /// </summary>
        public SystemMl(string modelFile, MlPolicy? policy = null)
        {
            if (!RuntimeInformation.IsOSPlatform(OSPlatform.Windows) &&
                !RuntimeInformation.IsOSPlatform(OSPlatform.Linux))
                throw new MlException(MlExceptionKind.Unavailable, "this ONNX backend is available on Windows and Linux");
            this.modelFile = modelFile ?? throw new ArgumentNullException(nameof(modelFile));
            this.policy = policy ?? new MlPolicy(modelFile);
            this.policy.Validate();
            if (!string.Equals(this.policy.ModelFile, modelFile, StringComparison.Ordinal) ||
                !string.Equals(this.policy.DeviceKind, "cpu", StringComparison.OrdinalIgnoreCase))
                throw new MlException(MlExceptionKind.Invalid, "this backend requires the selected model and cpu device");
        }

        /// <summary>
        /// Loads the ONNX model into an inference session. The session is bound to
        /// the exact model file supplied at construction.
        /// </summary>
        public MlSession LoadSession()
        {
            if (disposed) throw new ObjectDisposedException(nameof(SystemMl));
            try { return new MlSession(new InferenceSession(modelFile), policy); }
            catch (OnnxRuntimeException e) { throw new MlException(MlExceptionKind.NativeFailure, "load ONNX model failed", e); }
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
    /// One ONNX inference session bound to a loaded model.
    /// </summary>
    public sealed class MlSession : IDisposable
    {
        private readonly InferenceSession session;
        private readonly MlPolicy policy;
        private bool disposed;

        internal MlSession(InferenceSession session, MlPolicy policy) { this.session = session; this.policy = policy; }

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
            if (inputs.Length > policy.MaxInputTensors || inputs.Length != session.InputNames.Count ||
                session.OutputNames.Count < 1 || session.OutputNames.Count > policy.MaxOutputTensors)
                throw new MlException(MlExceptionKind.Invalid, "model tensor count exceeds policy or does not match inputs");
            foreach (var name in session.OutputNames)
            {
                var metadata = session.OutputMetadata[name];
                if (!metadata.IsTensor || metadata.ElementType != typeof(float) ||
                    metadata.Dimensions.Length < 1 || metadata.Dimensions.Length > 8)
                    throw new MlException(MlExceptionKind.Invalid, "output tensor type or rank is unsupported");
                long elements = 1;
                foreach (int dim in metadata.Dimensions)
                {
                    // Dynamic output dimensions cannot be bounded before the native run.
                    if (dim < 1 || elements > policy.MaxTensorBytes / 4L / dim)
                        throw new MlException(MlExceptionKind.Invalid, "output tensor shape is dynamic or exceeds size limit");
                    elements *= dim;
                }
            }
            var nativeInputs = new Dictionary<string, OrtValue>(inputs.Length);
            try
            {
                for (int i = 0; i < inputs.Length; i++)
                {
                    var input = inputs[i];
                    ValidateTensor(input, policy.MaxTensorBytes);
                    var metadata = session.InputMetadata[session.InputNames[i]];
                    if (!metadata.IsTensor || metadata.ElementType != typeof(float) ||
                        metadata.Dimensions.Length != input.Shape.Length ||
                        metadata.Dimensions.Where((dim, index) => dim > 0 && dim != input.Shape[index]).Any())
                        throw new MlException(MlExceptionKind.Invalid, "input tensor shape or type does not match model");
                    nativeInputs.Add(session.InputNames[i], OrtValue.CreateTensorValueFromMemory(
                        input.Float32Values(), input.Shape.Select(dim => (long)dim).ToArray()));
                }
                using var options = new RunOptions();
                using var outputs = session.Run(options, nativeInputs, session.OutputNames);
                var result = new List<MlTensor>(outputs.Count);
                foreach (var output in outputs)
                {
                    var shape = output.GetTensorTypeAndShape().Shape;
                    if (shape.Length < 1 || shape.Length > 8 || shape.Any(dim => dim < 1 || dim > int.MaxValue))
                        throw new MlException(MlExceptionKind.Invalid, "output tensor shape is unsupported");
                    var values = output.GetTensorDataAsSpan<float>();
                    if ((long)values.Length * 4 > policy.MaxTensorBytes)
                        throw new MlException(MlExceptionKind.Invalid, "output tensor exceeds size limit");
                    result.Add(MlTensor.FromFloat32(shape.Select(dim => (int)dim).ToArray(), values.ToArray()));
                }
                return result.ToArray();
            }
            catch (OnnxRuntimeException e)
            {
                throw new MlException(MlExceptionKind.NativeFailure, "ONNX inference failed", e);
            }
            finally
            {
                foreach (var value in nativeInputs.Values) value.Dispose();
            }
        }

        private static void ValidateTensor(MlTensor tensor, int maxBytes)
        {
            if (tensor.Format != "float32" || tensor.Shape.Length < 1 || tensor.Shape.Length > 8 ||
                tensor.Data.Length < 4 || tensor.Data.Length > maxBytes)
                throw new MlException(MlExceptionKind.Invalid, "input tensor format or size is invalid");
            long elements = 1;
            foreach (int dim in tensor.Shape)
            {
                if (dim < 1 || elements > maxBytes / 4L / dim)
                    throw new MlException(MlExceptionKind.Invalid, "input tensor shape exceeds size limit");
                elements *= dim;
            }
            if (elements * 4 != tensor.Data.Length)
                throw new MlException(MlExceptionKind.Invalid, "input tensor shape and bytes differ");
        }

        public void Dispose()
        {
            if (!disposed)
            {
                disposed = true;
                session.Dispose();
            }
        }
    }

    /// <summary>
    /// One multi-dimensional float32 tensor for ONNX inference.
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
