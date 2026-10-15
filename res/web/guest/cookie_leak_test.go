package guest

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/webong/ext/res/web/contract"
)

const querySecret = "s3cr3t-query-value-51ab"

func queryRequest(t *testing.T, request contract.CookieRequest) string {
	t.Helper()
	request.Version = contract.Version
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestCookieQueryNeverLeaksValuesOutsideTheResult(t *testing.T) {
	good := contract.Cookie{ID: 1, Name: "sid", Value: querySecret, Domain: "example.com", Path: "/"}
	badValue := contract.Cookie{ID: 2, Name: "bad", Value: querySecret + "\n", Domain: "example.com", Path: "/"}
	unreadable := contract.Cookie{ID: 3, Name: "locked", Domain: "example.com", Path: "/"}
	backend := CookieBackend{
		Query: func(profile string, site *url.URL, includeExpired bool) ([]contract.Cookie, string, error) {
			return []contract.Cookie{good, badValue, unreadable}, "handle", nil
		},
		ReadValue: func(handle string, cookie contract.Cookie) (string, error) {
			if cookie.ID == 3 {
				return "", errors.New("decryption failed")
			}
			return cookie.Value, nil
		},
	}
	var stdout, stderr strings.Builder
	input := queryRequest(t, contract.CookieRequest{Site: "https://example.com/"})
	if code := RunCookie("Default", "query", strings.NewReader(input), &stdout, &stderr, backend); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var result contract.CookieQueryResult
	if err := json.Unmarshal([]byte(stdout.String()), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Cookies) != 1 || result.Cookies[0].Value != querySecret {
		t.Fatalf("only the valid cookie may be returned: %+v", result)
	}
	if len(result.Warnings) != 2 {
		t.Fatalf("each skipped cookie needs a warning: %v", result.Warnings)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, querySecret) {
			t.Errorf("warning carries a value: %s", warning)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr must stay empty on success: %q", stderr.String())
	}
	// The printed forms of the decoded result are redacted too.
	if rendered := result.String(); strings.Contains(rendered, querySecret) {
		t.Fatalf("result String leaks: %s", rendered)
	}
}

func TestCookieQueryFailuresNeverLeakValues(t *testing.T) {
	secretError := errors.New("backend failed")
	backend := CookieBackend{
		Query:       func(string, *url.URL, bool) ([]contract.Cookie, string, error) { return nil, "", secretError },
		QueryValues: true,
	}
	var stdout, stderr strings.Builder
	request := contract.CookieRequest{Site: "https://example.com/", Cookie: contract.Cookie{Name: "x", Value: querySecret}}
	if code := RunCookie("Default", "query", strings.NewReader(queryRequest(t, request)), &stdout, &stderr, backend); code == 0 {
		t.Fatal("backend failure was accepted")
	}
	if strings.Contains(stderr.String(), querySecret) || strings.Contains(stdout.String(), querySecret) {
		t.Fatalf("a failure carries the request's value: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	// Malformed requests are rejected without echoing their content.
	stdout.Reset()
	stderr.Reset()
	body := `{"version":2,"site":"` + querySecret + `"}`
	if code := RunCookie("Default", "query", strings.NewReader(body), &stdout, &stderr, backend); code == 0 || strings.Contains(stderr.String(), querySecret) {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	// A query with no site and no allow_all_hosts is refused.
	stdout.Reset()
	stderr.Reset()
	if code := RunCookie("Default", "query", strings.NewReader(queryRequest(t, contract.CookieRequest{})), &stdout, &stderr, backend); code != 2 {
		t.Fatalf("an unfiltered query must be refused, got code %d", code)
	}
}

func TestCookieQueryResultIsBounded(t *testing.T) {
	cookies := make([]contract.Cookie, 0, 1100)
	for i := 0; i < 1100; i++ {
		cookies = append(cookies, contract.Cookie{ID: int64(i + 1), Name: "c" + strings.Repeat("x", 10), Value: strings.Repeat("v", 32<<10), Domain: "example.com", Path: "/"})
	}
	backend := CookieBackend{
		Query:       func(string, *url.URL, bool) ([]contract.Cookie, string, error) { return cookies, "h", nil },
		QueryValues: true,
	}
	var stdout, stderr strings.Builder
	if code := RunCookie("Default", "query", strings.NewReader(queryRequest(t, contract.CookieRequest{Site: "https://example.com/"})), &stdout, &stderr, backend); code != 1 {
		t.Fatalf("an oversize result must fail, got %d", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "size bound") {
		t.Fatalf("stdout=%d stderr=%q", stdout.Len(), stderr.String())
	}
	if contract.MaxCookieQueryResultBytes != 32<<20 {
		t.Fatal("the bound is part of the protocol")
	}
}
