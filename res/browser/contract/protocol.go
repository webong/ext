// Package contract exposes versioned browser resource and profile-management
// types shared by CTX hosts and browser guests. Native behavior remains in the
// browser adapters.
package contract

import (
	"encoding/json"
	"net/url"

	"github.com/webong/ctx/internal/app/browser/management"
	"github.com/webong/ctx/internal/app/browser/share"
)

const Version = share.Version
const AvailabilityVersion = share.AvailabilityVersion

type AvailabilityReport = share.AvailabilityReport
type Cookie = share.Cookie
type CookieBundle = share.CookieBundle
type CookieRequest = share.CookieRequest
type CookieQueryResult = share.CookieQueryResult
type ResourceBundle = share.ResourceBundle
type ResourceRequest = share.ResourceRequest
type PolicyEntry = share.PolicyEntry
type PolicyRequest = share.PolicyRequest
type PolicyBundle = share.PolicyBundle

type ManagementRequest = management.Request
type ManagementResponse = management.Response

// Request and Response keep existing browser management callers source-compatible.
type Request = management.Request
type Response = management.Response
type SessionTarget = management.SessionTarget
type InjectionOptions = management.InjectionOptions
type UserscriptRegistration = management.UserscriptRegistration
type ReplayResult = management.ReplayResult
type PageSessionRuntime = management.PageSessionRuntime
type PageSession = management.PageSession
type PageSessionState = management.PageSessionState

const ManagementVersion = management.Version

func NewManagementRequest(kind, action string, input any) (ManagementRequest, error) {
	return management.NewRequest(kind, action, input)
}

func ValidateManagementRequest(request ManagementRequest) error {
	return management.ValidateRequest(request)
}

func ValidateManagementResponse(response ManagementResponse, request ManagementRequest) error {
	return management.ValidateResponse(response, request)
}

func ManagementOperations() map[string][]string { return management.Operations() }

func ManagementResult(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func ParseSite(raw string) (*url.URL, error)         { return share.ParseSite(raw) }
func ValidateCookieBundle(bundle CookieBundle) error { return share.ValidateCookieBundle(bundle) }
func ValidateCookie(cookie Cookie) error             { return share.ValidateCookie(cookie) }
func ValidateResourceBundle(bundle ResourceBundle, resource string) error {
	return share.ValidateResourceBundle(bundle, resource)
}
func SameListedCookie(a, b Cookie) bool { return share.SameListedCookie(a, b) }
func CookieActive(cookie Cookie) bool   { return share.CookieActive(cookie) }
func CookieDomainMatches(siteHost, cookieDomain string) bool {
	return share.CookieDomainMatches(siteHost, cookieDomain)
}
func CookieMatchesSite(site *url.URL, cookie Cookie) bool {
	return share.CookieMatchesSite(site, cookie)
}
func CookieMatchesSiteOptions(site *url.URL, cookie Cookie, includeExpired bool) bool {
	return share.CookieMatchesSiteOptions(site, cookie, includeExpired)
}
func CookiePathMatches(requestPath, cookiePath string) bool {
	return share.CookiePathMatches(requestPath, cookiePath)
}
