package app

import (
	"reflect"
	"testing"

	modpkg "github.com/webong/ext/src/ctx/internal/mod"
)

func TestAdapterConfigurationLineEndings(t *testing.T) {
	candidate := &modpkg.Adapter{Manifest: modpkg.Manifest{
		Name: "fixture", SelectorKey: "fixture_context", ExtraKeys: []string{"fixture_namespace"},
	}}
	want := map[string]string{"fixture_context": "production", "fixture_namespace": "payments"}
	for _, test := range []struct{ name, output string }{
		{"unix", "fixture_context\tproduction\nfixture_namespace\tpayments\n"},
		{"windows", "fixture_context\tproduction\r\nfixture_namespace\tpayments\r\n"},
		{"mixed", "fixture_context\tproduction\r\nfixture_namespace\tpayments\n"},
		{"unterminated", "fixture_context\tproduction\nfixture_namespace\tpayments"},
		{"blank-lines", "\r\nfixture_context\tproduction\r\n\r\nfixture_namespace\tpayments\r\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseAdapterConfigurationValues(candidate, test.output)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("values=%v err=%v", got, err)
			}
		})
	}
}

func TestAdapterConfigurationRejectsInvalidRecords(t *testing.T) {
	candidate := &modpkg.Adapter{Manifest: modpkg.Manifest{
		Name: "fixture", SelectorKey: "fixture_context", ExtraKeys: []string{"fixture_namespace"},
	}}
	for _, output := range []string{
		"fixture_context\tproduction\rbroken\r\n",
		"fixture_context\tproduction\r\r\n",
		"fixture_context\tproduction\r\nundeclared\tvalue\r\n",
		"fixture_context\t\r\n",
		"fixture_context production\r\n",
		"fixture_namespace\tpayments\r\n",
		"",
	} {
		if _, err := parseAdapterConfigurationValues(candidate, output); err == nil {
			t.Fatalf("invalid record accepted: %q", output)
		}
	}
}
