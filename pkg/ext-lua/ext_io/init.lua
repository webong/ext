--- ext for Lua: one package, one namespace per area.
---
--- Only ext_io.plugin (the guest SDK for ext.plugin/v1) exists so far; system, web and int are
--- planned. Require the area you use (require("ext_io.plugin")); requiring ext_io loads none.
return { areas = { "plugin" } }
