# frozen_string_literal: true

# Runs every test: ruby test/all.rb
Dir.glob(File.join(__dir__, "test_*.rb")).sort.each { |f| require f }
