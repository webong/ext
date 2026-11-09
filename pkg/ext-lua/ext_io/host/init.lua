--- Host SDK for ext.plugin/v1: runs plugins through the C engine (lib/plugin).
---
---     local host = require("ext_io.host")
---
---     local engine = host.Engine()          -- EXT_HOST_LIBRARY, or libext_host on the loader path
---     local h = engine:process(descriptor, "/opt/plugins/echo/1.0.0/echo")
---     h:start()
---     h:call({ name = "ext.echo", version = "v1" }, "echo", { value = 7 })
---     h:destroy()                           -- or let h go out of scope: local h <close> = ...
---
--- The engine does the work a host must get right: admission, the handshake check against the
--- descriptor, request and response validation, deadlines and the session's lifecycle. This
--- module only moves bytes and policy decisions across a small C module, ext_io.host.core, which
--- loads the engine's shared library (libext_host) at run time. Requiring ext_io.plugin (the
--- guest) never loads either.
---
--- Guests may be JSON-line child processes (Engine:process) or libraries that export the C ABI of
--- ext_plugin.h (Engine:shared_library). Native code in a shared library runs inside this process
--- with its full access.
local core = require("ext_io.host.core")
local json = require("ext_io.json")
local plugin_core = require("ext_io.plugin.core")

local host = {
  ABI_VERSION = core.ABI_VERSION,
  STATUS_NAMES = core.STATUS_NAMES, -- 0-based: STATUS_NAMES[0] == "ok"
  remote_error = plugin_core.remote_error,
  is_remote_error = plugin_core.is_remote_error,
}

--- A host failure: the engine's status by name (status: "denied", "closed" ...), its number
--- (code) and what was being done (action).
local HostError = { __name = "ext_io.HostError" }
HostError.__index = HostError
HostError.__tostring = function(e)
  return "ext host: " .. (e.action ~= "" and (e.action .. ": ") or "") .. e.status
end
host.HostError = HostError

function host.is_host_error(v)
  return getmetatable(v) == HostError
end

--- The results of a pcall, with a failure the C module raised turned into a HostError, raised
--- again at level 0.
local function finish(ok, ...)
  if ok then return ... end
  local err = ...
  if type(err) == "table" and err.host_error then
    error(setmetatable({ code = err.code, status = err.status, action = err.action }, HostError), 0)
  end
  error(err, 0)
end

local function guard(f, ...)
  return finish(pcall(f, ...))
end

local function library_names()
  local sep = package.config:sub(1, 1)
  if sep == "\\" then return { "ext_host.dll" } end
  local uname = io.popen and io.popen("uname -s 2>/dev/null")
  local system = uname and uname:read("*l") or ""
  if uname then uname:close() end
  if system == "Darwin" then return { "libext_host.dylib" } end
  return { "libext_host.so" }
end

--- The C engine, loaded from path, EXT_HOST_LIBRARY, or libext_host on the loader path. Raises
--- a string error when no library loads or its ABI is not this module's.
function host.Engine(path)
  path = path or os.getenv("EXT_HOST_LIBRARY")
  if path and path ~= "" then
    return setmetatable({ _engine = core.open(path) }, host.Engine_mt)
  end
  local last
  for _, name in ipairs(library_names()) do
    local ok, engine = pcall(core.open, name)
    if ok then return setmetatable({ _engine = engine }, host.Engine_mt) end
    last = engine
  end
  error("the ext engine library was not found: set EXT_HOST_LIBRARY to libext_host, or pass its path ("
    .. tostring(last) .. ")", 2)
end

local Engine = {}
host.Engine_mt = { __index = Engine, __name = "ext_io.host.Engine" }

local function encode(value)
  if type(value) == "string" then return value end
  return json.encode(value)
end

--- A policy for the C module: decide(table) -> truthy allows. The C module denies on an error.
local function policy(decide)
  if decide == nil then return nil end
  return function(data)
    return decide(json.decode(data)) and true or false
  end
end

local Host = {}
local Host_mt = { __index = Host, __name = "ext_io.host.Host" }
Host_mt.__close = function(h) h:destroy() end
host.Host_mt = Host_mt

local function wrap(raw)
  return setmetatable({ _host = raw }, Host_mt)
end

--- A host for a JSON-line guest started as executable (an absolute path, no shell). opts holds
--- arguments and environment (arrays of strings; environment entries are NAME=value added to a
--- scrubbed base of PATH), working_directory, stderr_bytes (default 4096; read with
--- Host:stderr()), and the policies verify (sees the descriptor before the child starts) and
--- authorize (each validated request). A policy returns true to allow; an error denies. Neither
--- may block or use this host.
function Engine:process(descriptor, executable, opts)
  opts = opts or {}
  return wrap(guard(self._engine.process, self._engine, encode(descriptor), executable,
    opts.arguments, opts.environment, opts.working_directory, opts.stderr_bytes,
    policy(opts.verify), policy(opts.authorize)))
end

--- A host for a guest library that exports the C ABI of ext_plugin.h. path is an absolute path to
--- a verified, revision-specific artifact; it is loaded when the host starts, after verify, and is
--- never unloaded. opts holds verify and authorize, as for Engine:process.
function Engine:shared_library(descriptor, path, opts)
  opts = opts or {}
  return wrap(guard(self._engine.shared_library, self._engine, encode(descriptor), path,
    policy(opts.verify), policy(opts.authorize)))
end

--- Starts the guest and checks its handshake against the descriptor. timeout is in seconds
--- (default 3), as for every call below.
function Host:start(timeout)
  guard(self._host.start, self._host, timeout)
end

--- Calls an operation and returns its payload (nil when it has none). A domain error raises the
--- guest SDK's RemoteError (host.is_remote_error); a host failure raises a HostError.
function Host:call(contract, operation, payload, timeout)
  local request = json.object({
    contract = json.object({ name = contract.name, version = contract.version }, { "name", "version" }),
    operation = operation,
  }, { "contract", "operation" })
  if payload ~= nil and payload ~= json.null then
    request.payload = payload
    json.object(request, { "contract", "operation", "payload" })
  end
  local response = json.decode(guard(self._host.call_json, self._host, json.encode(request), timeout, operation))
  local e = response.error
  if e ~= nil and e ~= json.null then
    local retry = e.retryAfterMilliseconds
    error(setmetatable({ code = e.code, message = e.message,
      retry_after_ms = retry ~= json.null and retry or nil }, plugin_core.RemoteError), 0)
  end
  local result = response.payload
  if result == json.null then return nil end
  return result
end

--- Sends a complete request envelope (a JSON string) and returns the response envelope (a string).
function Host:invoke(request, timeout)
  return guard(self._host.invoke, self._host, request, timeout)
end

--- Stops admission and waits for the calls in flight; a timeout reopens admission.
function Host:drain(timeout)
  guard(self._host.drain, self._host, timeout)
end

--- Aborts the session; calls in flight end with "closed". Harmless after destroy.
function Host:close()
  guard(self._host.close, self._host)
end

--- "created", "ready", "draining", "closed" or "failed".
function Host:state()
  return guard(self._host.state, self._host)
end

--- What a process guest wrote to stderr, bounded and untrusted.
function Host:stderr()
  return guard(self._host.stderr, self._host)
end

--- Frees the host. Once is enough; again is harmless. Garbage collection does it too.
function Host:destroy()
  guard(self._host.destroy, self._host)
end

return host
