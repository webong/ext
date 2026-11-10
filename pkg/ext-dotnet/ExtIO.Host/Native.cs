using System.Runtime.InteropServices;

namespace ExtIO.Host;

// The parts of lib/plugin/include/ext_host.h (host ABI 2) this binding uses, as blittable structs
// and unmanaged function pointers resolved from one loaded engine. The ABI version check and the
// shared host cases hold them to the header.

[StructLayout(LayoutKind.Sequential)]
internal unsafe struct ExtBuffer
{
    public byte* Data;
    public nuint Len;
}

[StructLayout(LayoutKind.Sequential)]
internal unsafe struct ExtHostOptions
{
    public uint AbiVersion;
    public uint StructSize;
    public byte* Descriptor;
    public nuint DescriptorLen;
    public delegate* unmanaged<void*, byte*, nuint, int> Verify;
    public delegate* unmanaged<void*, byte*, nuint, int> Authorize;
    public void* User;
}

[StructLayout(LayoutKind.Sequential)]
internal unsafe struct ExtBackendOptions
{
    public uint StructSize;
    public uint Kind;
    public void* Config;
    public nuint ConfigSize;
}

[StructLayout(LayoutKind.Sequential)]
internal unsafe struct ExtProcessLaunch
{
    public uint StructSize;
    public byte* Executable;
    public byte** Arguments;
    public nuint ArgumentCount;
    public byte** Environment;
    public nuint EnvironmentCount;
    public byte* WorkingDirectory;
    public uint StderrBytes;
}

[StructLayout(LayoutKind.Sequential)]
internal unsafe struct ExtCallOptions
{
    public uint StructSize;
    public uint TimeoutMs;
    public void* Cancel;
    public void* Value;
}

[StructLayout(LayoutKind.Sequential)]
internal unsafe struct ExtExtensionContext
{
    public uint StructSize;
    public uint Flags;
    public void* User;
    public void* Connect;
    public void* Invoke;
    public void* Close;
    public delegate* unmanaged<void*, void> Release;
}

[StructLayout(LayoutKind.Sequential)]
internal unsafe struct ExtSharedLibraryOptions
{
    public uint StructSize;
    public byte* Path;
}

/// <summary>The engine's functions, resolved once per loaded library.</summary>
internal sealed unsafe class NativeApi
{
    public const uint AbiVersion = 2;
    public const uint BackendExtensionContext = 3;
    public const uint BackendProcessLaunch = 5;

    public readonly delegate* unmanaged<uint> AbiVersionOf;
    public readonly delegate* unmanaged<ExtHostOptions*, ExtBackendOptions*, nint*, int> Create;
    public readonly delegate* unmanaged<nint, ExtCallOptions*, int> Start;
    public readonly delegate* unmanaged<nint, byte*, nuint, ExtCallOptions*, ExtBuffer*, int> Call;
    public readonly delegate* unmanaged<nint, byte*, nuint, ExtCallOptions*, ExtBuffer*, int> Invoke;
    public readonly delegate* unmanaged<nint, ExtCallOptions*, int> Drain;
    public readonly delegate* unmanaged<nint, void> Close;
    public readonly delegate* unmanaged<nint, void> Destroy;
    public readonly delegate* unmanaged<nint, int> State;
    public readonly delegate* unmanaged<nint, byte*, nuint, nuint*, int> Stderr;
    public readonly delegate* unmanaged<ExtBuffer*, void> BufferFree;
    public readonly delegate* unmanaged<ExtSharedLibraryOptions*, ExtExtensionContext*, int> SharedLibrary;
    public readonly delegate* unmanaged<nint*, int> CancelCreate;
    public readonly delegate* unmanaged<nint, void> CancelSignal;
    public readonly delegate* unmanaged<nint, void> CancelDestroy;

    public NativeApi(nint library, string path)
    {
        nint Export(string name) => NativeLibrary.TryGetExport(library, name, out var address)
            ? address
            : throw new DllNotFoundException($"{path} is not an ext engine library: {name} is missing");

        AbiVersionOf = (delegate* unmanaged<uint>)Export("ext_host_abi_version");
        var abi = AbiVersionOf();
        if (abi != AbiVersion)
        {
            throw new DllNotFoundException($"ext engine ABI {abi}, this binding needs {AbiVersion}");
        }
        Create = (delegate* unmanaged<ExtHostOptions*, ExtBackendOptions*, nint*, int>)Export("ext_host_create");
        Start = (delegate* unmanaged<nint, ExtCallOptions*, int>)Export("ext_host_start_with_options");
        Call = (delegate* unmanaged<nint, byte*, nuint, ExtCallOptions*, ExtBuffer*, int>)Export("ext_host_call");
        Invoke = (delegate* unmanaged<nint, byte*, nuint, ExtCallOptions*, ExtBuffer*, int>)Export("ext_host_invoke_with_options");
        Drain = (delegate* unmanaged<nint, ExtCallOptions*, int>)Export("ext_host_drain_with_options");
        Close = (delegate* unmanaged<nint, void>)Export("ext_host_close");
        Destroy = (delegate* unmanaged<nint, void>)Export("ext_host_destroy");
        State = (delegate* unmanaged<nint, int>)Export("ext_host_get_state");
        Stderr = (delegate* unmanaged<nint, byte*, nuint, nuint*, int>)Export("ext_host_process_stderr");
        BufferFree = (delegate* unmanaged<ExtBuffer*, void>)Export("ext_buffer_free");
        SharedLibrary = (delegate* unmanaged<ExtSharedLibraryOptions*, ExtExtensionContext*, int>)Export("ext_shared_library_backend_create");
        CancelCreate = (delegate* unmanaged<nint*, int>)Export("ext_cancel_create");
        CancelSignal = (delegate* unmanaged<nint, void>)Export("ext_cancel_signal");
        CancelDestroy = (delegate* unmanaged<nint, void>)Export("ext_cancel_destroy");
    }
}
