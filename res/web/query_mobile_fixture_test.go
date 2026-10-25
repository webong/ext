package web

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestMobileQueryFixtures(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name, URL, Mode                           string
			Origins, Names                            []string
			InlineOnly, IncludeExpired, AllowAllHosts bool
			Inline, Fallback                          json.RawMessage
			Sources                                   []struct {
				Label     string
				Cookies   json.RawMessage
				Warnings  []string
				StorePath string
			}
			Expected []struct{ Value, Source string }
			Calls    []string
		}
	}
	data, err := os.ReadFile("testdata/cookie/query.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, v := range fixture.Cases {
		t.Run(v.Name, func(t *testing.T) {
			calls := []string{}
			sources := []Source{}
			for _, s := range v.Sources {
				sources = append(sources, Source{Adapter: "test", Profile: "work", Label: s.Label, SupportsQuery: true,
					Invoke: func(context.Context, string, []string, []byte) ([]byte, error) {
						calls = append(calls, s.Label)
						return json.Marshal(map[string]any{"cookies": s.Cookies, "warnings": s.Warnings, "store_path": s.StorePath})
					}})
			}
			r, err := Get(context.Background(), Options{URL: v.URL, Origins: v.Origins, Names: v.Names, Mode: Mode(v.Mode), InlineOnly: v.InlineOnly,
				IncludeExpired: v.IncludeExpired, AllowAllHosts: v.AllowAllHosts, Inline: InlineCookies{Data: v.Inline}, FallbackInline: InlineCookies{Data: v.Fallback}, Backend: queryBackend{sources: sources}})
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Cookies) != len(v.Expected) {
				t.Fatalf("cookie count %d want %d", len(r.Cookies), len(v.Expected))
			}
			for i, want := range v.Expected {
				if r.Cookies[i].Value != want.Value || r.Cookies[i].Source != want.Source {
					t.Fatalf("entry %d differs", i)
				}
			}
			if !reflect.DeepEqual(calls, v.Calls) {
				t.Fatalf("calls %v want %v", calls, v.Calls)
			}
		})
	}
}
