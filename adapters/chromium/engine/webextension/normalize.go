// Package webextension decodes the browser cookies API export format. Product
// adapters supply their identity and supported scope fields. No OS store or
// browser credential is accessed while normalizing an authorized export.
package webextension

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	browser "github.com/webong/ext/res/web/contract"
)

type Policy struct {
	Browser, Namespace          string
	Partition, FirstPartyDomain bool
}

// Normalize checks the asserted runtime binding against the caller's selected
// endpoint. The application must obtain that binding from its trusted runtime;
// an exported file alone cannot prove a browser profile's identity.
func Normalize(policy Policy, profile, storeID string, data json.RawMessage) (browser.CookieQueryResult, error) {
	var envelope struct {
		Selection        struct{ Browser, Profile, StoreID string }
		Sites            []string
		Names            []string
		Partition        json.RawMessage
		FirstPartyDomain *string
		Cookies          []json.RawMessage
	}
	if len(data) > 8<<20 || !utf8.Valid(data) || json.Unmarshal(data, &envelope) != nil || envelope.Cookies == nil {
		return browser.CookieQueryResult{}, errors.New("native export must contain a bounded UTF-8 cookie export object")
	}
	if policy.Browser == "" || policy.Namespace == "" || profile == "" || !validStoreID(storeID) ||
		envelope.Selection.Browser != policy.Browser || envelope.Selection.Profile != profile || envelope.Selection.StoreID != storeID {
		return browser.CookieQueryResult{}, errors.New("native export browser, profile, or store does not match the selected endpoint")
	}
	if len(envelope.Sites) == 0 || len(envelope.Sites) > 16 || len(envelope.Names) == 0 || len(envelope.Names) > 32 {
		return browser.CookieQueryResult{}, errors.New("native export needs 1–16 sites and 1–32 cookie names")
	}
	for _, site := range envelope.Sites {
		if _, err := browser.ParseSite(site); err != nil {
			return browser.CookieQueryResult{}, errors.New("native export has an invalid site")
		}
	}
	names := make(map[string]bool)
	for _, name := range envelope.Names {
		if name == "" || len(name) > 256 || strings.ContainsAny(name, "()<>@,;:\\\"/[]?={} \t") || strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return browser.CookieQueryResult{}, errors.New("native export has an invalid name selection")
		}
		names[name] = true
	}
	selectedPartition, err := partitionSite(envelope.Partition, true)
	if err != nil {
		return browser.CookieQueryResult{}, err
	}
	if selectedPartition != "" && !policy.Partition {
		return browser.CookieQueryResult{}, errors.New("adapter does not support this export's partition scope")
	}
	if envelope.FirstPartyDomain != nil && !policy.FirstPartyDomain {
		return browser.CookieQueryResult{}, errors.New("adapter does not support first-party domain selection")
	}
	result := browser.CookieQueryResult{Cookies: make([]browser.Cookie, 0, len(envelope.Cookies)), StoreID: storeID}
	for index, row := range envelope.Cookies {
		cookie, err := normalizeCookie(policy, storeID, selectedPartition, envelope.FirstPartyDomain, row)
		if err == nil && !names[cookie.Name] {
			err = errors.New("cookie is outside the selected names")
		}
		if err == nil {
			matches := false
			for _, rawSite := range envelope.Sites {
				site, _ := browser.ParseSite(rawSite)
				if browser.CookieMatchesSiteOptions(site, cookie, true) {
					matches = true
					break
				}
			}
			if !matches {
				err = errors.New("cookie is outside the selected sites")
			}
		}
		if err != nil {
			return browser.CookieQueryResult{}, fmt.Errorf("native export cookie %d: %w", index+1, err)
		}
		result.Cookies = append(result.Cookies, cookie)
	}
	return result, nil
}

func normalizeCookie(policy Policy, storeID, selectedPartition string, selectedFirstParty *string, data json.RawMessage) (browser.Cookie, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return browser.Cookie{}, errors.New("invalid cookie object")
	}
	var cookie browser.Cookie
	for name, target := range map[string]*string{"name": &cookie.Name, "value": &cookie.Value, "domain": &cookie.Domain, "path": &cookie.Path} {
		if !decode(fields[name], target) {
			return cookie, errors.New("cookie needs name, value, domain, and path strings")
		}
	}
	var hostOnly, session bool
	for name, target := range map[string]*bool{"secure": &cookie.Secure, "httpOnly": &cookie.HTTPOnly, "hostOnly": &hostOnly, "session": &session} {
		if !decode(fields[name], target) {
			return cookie, errors.New("cookie needs complete browser boolean fields")
		}
	}
	var rowStore, sameSite string
	if !decode(fields["storeId"], &rowStore) || rowStore != storeID || !decode(fields["sameSite"], &sameSite) {
		return cookie, errors.New("cookie store or SameSite field is invalid")
	}
	cookie.Domain = strings.TrimPrefix(cookie.Domain, ".")
	if !hostOnly {
		cookie.Domain = "." + cookie.Domain
	}
	switch sameSite {
	case "no_restriction":
		cookie.SameSitePolicy = "none"
	case "unspecified", "lax", "strict":
		cookie.SameSitePolicy = sameSite
	default:
		return cookie, errors.New("unsupported browser SameSite value")
	}
	if session {
		if fields["expirationDate"] != nil {
			return cookie, errors.New("session cookie has a persistent expiry")
		}
	} else {
		var expiry float64
		if !decode(fields["expirationDate"], &expiry) || math.IsNaN(expiry) || math.IsInf(expiry, 0) || expiry > 253402300799 {
			return cookie, errors.New("persistent cookie has an invalid expiry")
		}
		if expiry < 1 {
			cookie.Expiry = -1
		} else {
			cookie.Expiry = int64(math.Floor(expiry))
		}
	}
	// The browser API store remains explicit: it may identify an incognito,
	// contextual-identity, or provider-specific store. Never collapse it into a
	// disk-profile scope based on an assumed default store ID.
	cookie.Attributes = map[string]string{policy.Namespace + ".webextension_store_id": storeID}
	partition, err := partitionSite(fields["partitionKey"], false)
	if err != nil {
		return cookie, err
	}
	if partition != selectedPartition {
		return cookie, errors.New("cookie is outside the selected partition")
	}
	if partition != "" {
		if !policy.Partition {
			return cookie, errors.New("adapter cannot preserve this partition scope")
		}
		cookie.PartitionKey = partition
		var object map[string]json.RawMessage
		_ = json.Unmarshal(fields["partitionKey"], &object)
		if raw := object["hasCrossSiteAncestor"]; raw != nil && !decode(raw, &cookie.CrossSiteAncestor) {
			return cookie, errors.New("invalid partition ancestry")
		}
		if err := attribute(cookie.Attributes, policy.Namespace+".webextension_partition_key", fields["partitionKey"]); err != nil {
			return cookie, err
		}
	}
	var firstParty string
	if raw := fields["firstPartyDomain"]; raw != nil && !decode(raw, &firstParty) {
		return cookie, errors.New("invalid first-party domain")
	}
	if selectedFirstParty != nil && firstParty != *selectedFirstParty || selectedFirstParty == nil && firstParty != "" {
		return cookie, errors.New("cookie is outside the selected first-party domain")
	}
	if firstParty != "" {
		if !policy.FirstPartyDomain {
			return cookie, errors.New("adapter cannot preserve first-party isolation")
		}
		cookie.Attributes[policy.Namespace+".webextension_first_party_domain"] = firstParty
	}
	for name, value := range fields {
		switch name {
		case "name", "value", "domain", "path", "secure", "httpOnly", "hostOnly", "session", "storeId", "sameSite", "expirationDate", "partitionKey", "firstPartyDomain":
			continue
		}
		if err := attribute(cookie.Attributes, policy.Namespace+".webextension_extra."+name, value); err != nil {
			return cookie, err
		}
	}
	if err := browser.ValidateCookie(cookie); err != nil {
		return cookie, err
	}
	return cookie, nil
}

func decode(data json.RawMessage, target any) bool {
	return len(data) > 0 && !bytes.Equal(bytes.TrimSpace(data), []byte("null")) && json.Unmarshal(data, target) == nil
}

func validStoreID(value string) bool {
	return value != "" && len(value) <= 256 && !strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

func partitionSite(data json.RawMessage, selection bool) (string, error) {
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return "", nil
	}
	if selection && bytes.Equal(bytes.TrimSpace(data), []byte(`"unpartitioned"`)) {
		return "", nil
	}
	var object map[string]json.RawMessage
	var site string
	if json.Unmarshal(data, &object) != nil || !decode(object["topLevelSite"], &site) || site == "" {
		return "", errors.New("partition scope needs a concrete top-level site")
	}
	parsed, err := browser.ParseSite(site)
	if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("partition scope needs an HTTP(S) origin")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func attribute(attributes map[string]string, key string, data json.RawMessage) error {
	var compact bytes.Buffer
	if json.Compact(&compact, data) != nil || compact.Len() > 1024 || len(key) > 128 || strings.ContainsFunc(key, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return errors.New("native cookie metadata exceeds the portable attribute limits")
	}
	attributes[key] = compact.String()
	return nil
}
