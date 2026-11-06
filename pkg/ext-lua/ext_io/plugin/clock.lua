--- The clocks the guest reads, in pure Lua.
--
-- `now()` is the wall clock in seconds since the Unix epoch (the scale of request deadlines).
-- `monotonic_ns()` is a clock in nanoseconds that never goes back, for the progress rate limit.
--
-- Standard Lua has no sub-second wall clock, so the source is chosen once, at load time:
--
--   * LuaJIT: the C library through the built-in FFI, `clock_gettime` (CLOCK_REALTIME and
--     CLOCK_MONOTONIC) on POSIX systems, `GetSystemTimeAsFileTime` and `QueryPerformanceCounter`
--     on Windows. `source` is "ffi".
--   * Otherwise `os.time()`, whole seconds; the monotonic clock is the same in nanoseconds.
--     `source` is "os.time". A deadline is then honoured to within a second.
--
-- A guest that has a better clock (LuaSocket's `socket.gettime`, luaposix) passes it as the
-- `clock` option of `Guest`/`serve_stdio`; nothing here loads a C module.
local clock = {}

local function from_os_time()
  return os.time() + 0.0
end

clock.source = "os.time"
clock.now = from_os_time
clock.monotonic_ns = function() return os.time() * 1e9 end

local ok, ffi = pcall(require, "ffi")
if ok and type(ffi) == "table" and ffi.cdef and jit then
  pcall(function()
    if ffi.os == "Windows" then
      ffi.cdef [[
        typedef struct { uint32_t low; uint32_t high; } ext_io_filetime;
        void GetSystemTimeAsFileTime(ext_io_filetime *);
        int QueryPerformanceCounter(int64_t *);
        int QueryPerformanceFrequency(int64_t *);
      ]]
      local ft = ffi.new("ext_io_filetime")
      local counter, frequency = ffi.new("int64_t[1]"), ffi.new("int64_t[1]")
      local function now()
        ffi.C.GetSystemTimeAsFileTime(ft)
        -- 100 ns ticks since 1601-01-01.
        return (tonumber(ft.high) * 4294967296 + tonumber(ft.low)) / 1e7 - 11644473600
      end
      assert(ffi.C.QueryPerformanceFrequency(frequency) ~= 0)
      local hz = tonumber(frequency[0])
      local function mono()
        ffi.C.QueryPerformanceCounter(counter)
        return tonumber(counter[0]) / hz * 1e9
      end
      assert(now() > 0 and mono() >= 0)
      clock.now, clock.monotonic_ns, clock.source = now, mono, "ffi"
    else
      ffi.cdef [[
        typedef struct { long tv_sec; long tv_nsec; } ext_io_timespec;
        int clock_gettime(int, ext_io_timespec *);
      ]]
      local monotonic_id = ({ OSX = 6, Linux = 1, BSD = 4 })[ffi.os]
      local ts = ffi.new("ext_io_timespec")
      local function read(id)
        if ffi.C.clock_gettime(id, ts) ~= 0 then error("clock_gettime failed") end
        return tonumber(ts.tv_sec), tonumber(ts.tv_nsec)
      end
      local function now()
        local s, ns = read(0) -- CLOCK_REALTIME
        return s + ns / 1e9
      end
      assert(math.abs(now() - os.time()) < 5)
      local mono = function() return now() * 1e9 end
      if monotonic_id then
        read(monotonic_id)
        mono = function()
          local s, ns = read(monotonic_id)
          return s * 1e9 + ns
        end
      end
      clock.now, clock.monotonic_ns, clock.source = now, mono, "ffi"
    end
  end)
end

return clock
