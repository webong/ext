//go:build !darwin

package main

import (
	"errors"
	"net/url"

	browsershare "github.com/webong/ctx/res/browser/contract"
)

func safariShareStatus(string) map[string]string {
	return map[string]string{"policy.export": "blocked", "cookie.list": "blocked", "cookie.export": "blocked", "cookie.query": "blocked"}
}

func readSafariSiteCookies(string, *url.URL, string) ([]browsershare.Cookie, string, error) {
	return nil, "", errors.New("Safari cookies are only available on macOS")
}

func querySafariCookies(string, *url.URL, string, bool, bool) ([]browsershare.Cookie, string, error) {
	return nil, "", errors.New("Safari cookies are only available on macOS")
}

func readSafariCookieValue(string, browsershare.Cookie) (string, error) {
	return "", errors.New("Safari cookies are only available on macOS")
}
