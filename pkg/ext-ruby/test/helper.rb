# frozen_string_literal: true

$LOAD_PATH.unshift(File.expand_path("../lib", __dir__))
require "ext_io/plugin"
require "minitest/autorun"
require "json"
require "stringio"

module TestHelper
  P = ExtIO::Plugin
  FIXTURES = File.expand_path("../../../lib/plugin/testdata/v1", __dir__)

  module_function

  def fixture(name)
    JSON.parse(File.read(File.join(FIXTURES, name), encoding: "UTF-8"))
  end

  # Frames as the host writes them, one JSON object per line.
  def frames(*objects)
    objects.map { |o| JSON.generate(o) + "\n" }.join.b
  end
end
