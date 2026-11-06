# frozen_string_literal: true

# Builds ExtIO::Host::Native (ext_io/host/native), the extension under the Ruby host. It needs the
# Ruby headers and the C engine's ext_host.h only: the engine's shared library is loaded at run
# time. The header is found with --with-ext_host-include=DIR or --with-ext_host-dir=DIR (an
# unpacked ext-engine release), EXT_HOST_INCLUDE, or the lib/plugin/include of a checkout.
require "mkmf"

checkout = File.expand_path("../../../../lib/plugin/include", __dir__)
dir_config("ext_host")
[ENV["EXT_HOST_INCLUDE"], checkout].compact.each do |dir|
  next unless File.file?(File.join(dir, "ext_host.h"))

  $INCFLAGS << " -I#{dir.shellescape}" # rubocop:disable Style/GlobalVars
  break
end
abort "ext_host.h was not found: pass --with-ext_host-include=DIR or set EXT_HOST_INCLUDE" unless have_header("ext_host.h")
have_library("dl", "dlopen") unless RUBY_PLATFORM.match?(/mswin|mingw|darwin/)

create_makefile("ext_io/host/native")
