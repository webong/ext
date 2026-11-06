# frozen_string_literal: true

# ext for Ruby: one gem, one namespace per area.
#
# Only ExtIO::Plugin (the guest SDK for ext.plugin/v1, require "ext_io/plugin") exists so far;
# +system+, +web+ and +int+ are planned. Require the area you use; requiring "ext_io" loads none
# of them.
module ExtIO
end
