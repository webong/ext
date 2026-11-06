# frozen_string_literal: true

require_relative "lib/ext_io/plugin/version"

Gem::Specification.new do |spec|
  spec.name = "ext-io"
  spec.version = ExtIO::Plugin::VERSION
  spec.authors = ["Wisdom Ebong", "ext contributors"]
  spec.summary = "ext for Ruby: the plugin guest SDK for ext.plugin/v1 over JSON lines (standard library only)"
  spec.description = <<~TEXT
    One gem for ext's APIs, one namespace per area. ExtIO::Plugin (require "ext_io/plugin") is a
    guest for the ext.plugin/v1 protocol over JSON lines on standard input and output: the
    handshake, strict request validation, structured errors, deadlines, progress events and calls
    back to the host. It needs nothing beyond Ruby's standard library and no native build.
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
  spec.files = Dir.chdir(__dir__) { Dir["lib/**/*.rb"] + %w[README.md LICENSE] }
  spec.require_paths = ["lib"]
end
