package web

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/webong/ext/res/web/contract"
)

func TestCookieWorkflowFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/cookie/v2/workflows.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name        string            `json:"name"`
		Input       string            `json:"input"`
		Valid       bool              `json:"valid"`
		Cookies     []contract.Cookie `json:"cookies"`
		Site        string            `json:"site"`
		Header      *string           `json:"header"`
		HeaderError bool              `json:"header_error"`
		Jar         *string           `json:"jar"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			cookies, err := ParseCookies([]byte(tc.Input))
			if (err == nil) != tc.Valid {
				t.Fatalf("parse success = %v, want %v", err == nil, tc.Valid)
			}
			if err != nil {
				if strings.Contains(err.Error(), "fixture-token") {
					t.Fatal("error leaked value")
				}
				return
			}
			if len(cookies) != len(tc.Cookies) {
				t.Fatal("cookie count differs")
			}
			for i, cookie := range cookies {
				if !reflect.DeepEqual(cookie.Cookie, tc.Cookies[i]) {
					t.Fatalf("cookie %d differs", i)
				}
			}
			if tc.Header != nil || tc.HeaderError {
				got, err := CookieHeader(cookies, tc.Site)
				if (err != nil) != tc.HeaderError {
					t.Fatal("header error differs")
				}
				if tc.Header != nil && got != *tc.Header {
					t.Fatal("header differs")
				}
			}
			if tc.Jar != nil {
				got, err := NetscapeCookies(cookies)
				if err != nil || got != *tc.Jar {
					t.Fatal("jar differs")
				}
			}
		})
	}
}
