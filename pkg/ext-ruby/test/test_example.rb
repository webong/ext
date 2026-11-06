# frozen_string_literal: true

require_relative "helper"
require "digest"
require "open3"
require "rbconfig"

# The example plugin runs, and its manifest names the script it ships.
class ExampleTest < Minitest::Test
  include TestHelper
  DIR = File.expand_path("../examples/echo", __dir__)

  def test_the_manifest_digest_matches_the_script
    manifest = JSON.parse(File.read(File.join(DIR, "plugin.json")))
    artifact = manifest["artifacts"].find { |a| a["name"] == "main" }
    assert_equal Digest::SHA256.file(File.join(DIR, artifact["path"])).hexdigest, artifact["sha256"]
  end

  def test_the_example_serves
    ident = { "id" => "example/ruby-echo", "revision" => "fixture-1" }
    base = { "apiVersion" => "ext.plugin/v1", "plugin" => ident, "deadline" => "2999-01-01T00:00:00Z" }
    input = frames(
      { "apiVersion" => "ext.plugin/v1", "id" => "hello", "operation" => "plugin.hello", "deadline" => "2999-01-01T00:00:00Z" },
      base.merge("id" => "1", "contract" => { "name" => "example.echo", "version" => "v1" }, "operation" => "count",
                 "payload" => { "to" => 2 }, "progress" => {}),
      base.merge("id" => "2", "contract" => { "name" => "ext.health", "version" => "v1" }, "operation" => "check",
                 "surface" => "observation", "payload" => {})
    )
    out, err, status = Open3.capture3(RbConfig.ruby, "--disable-gems", File.join(DIR, "main.rb"), stdin_data: input, binmode: true)
    assert status.success?, err
    lines = out.lines.map { |l| JSON.parse(l) }
    assert_equal %w[hello progress progress 1 2], lines.map { |l| l["frame"] || l["id"] }
    assert_equal({ "counted" => 2 }, lines[3]["payload"])
    assert_equal({ "status" => "healthy", "ready" => true }, lines[4]["payload"])
  end
end
