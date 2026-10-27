package main

import "testing"

func TestValidateRunAcceptsKnownOperations(t *testing.T) {
	for _, args := range [][]string{
		{"status", "example-agent.service"},
		{"show", "example-agent.service"},
		{"start", "example-agent.service"},
		{"daemon-reload"},
	} {
		if err := validateRun(args); err != nil {
			t.Errorf("%v = %v", args, err)
		}
	}
}

func TestValidateRunRejectsArbitraryCommands(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"rm"},
		{"status", "--now"},
		{"status", "../escape"},
		{"status", "bad\x00unit"},
		{"status", ""},
	} {
		if err := validateRun(args); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
}
