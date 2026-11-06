# frozen_string_literal: true

require_relative "lib/ext_io/plugin/version"

# The host SDK (ExtIO::Host, require "ext_io/host"): an opt-in gem beside the guest (ext-io), with
# a native extension that loads the C engine's shared library (libext_host) at run time. The
# extension needs ext_host.h to build: a checkout of the repository has it, and otherwise pass
# --with-ext_host-dir=<an unpacked ext-engine release> or set EXT_HOST_INCLUDE.
Gem::Specification.new do |spec|
  spec.name = "ext-io-host"
  spec.version = ExtIO::Plugin::VERSION
  spec.authors = ["Wisdom Ebong", "ext contributors"]
  spec.summary = "ext for Ruby: the host SDK for ext.plugin/v1 on the ext C engine"
  spec.description = <<~TEXT
    Runs ext plugins (JSON-line processes and C ABI libraries) from Ruby through the ext C engine,
    which does admission, handshake checks, validation, deadlines and the session lifecycle. Calls
    run without the global VM lock. Needs the engine's shared library, libext_host, from an
    ext-engine release or a CMake build.
  TEXT
  spec.homepage = "https://github.com/webong/ext/tree/main/pkg/ext-ruby"
  spec.license = "MIT"
  spec.required_ruby_version = ">= 3.1"
  spec.metadata = {
    "homepage_uri" => spec.homepage,
    "source_code_uri" => "https://github.com/webong/ext",
    "bug_tracker_uri" => "https://github.com/webong/ext/issues",
    "rubygems_mfa_required" => "true"
  }
  spec.files = Dir.chdir(__dir__) { %w[lib/ext_io/host.rb ext/ext_io_host/extconf.rb ext/ext_io_host/native.c README.md LICENSE] }
  spec.extensions = ["ext/ext_io_host/extconf.rb"]
  spec.require_paths = ["lib"]
  spec.add_dependency "ext-io", "= #{ExtIO::Plugin::VERSION}"
end
