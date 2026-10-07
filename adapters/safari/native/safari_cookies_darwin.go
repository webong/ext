//go:build darwin

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	browsershare "github.com/webong/ext/res/browser/contract"
)

const maxSafariCookieStoreSize = 128 << 20
const safariEpochUnix = 978307200

type safariCookieRecord struct {
	cookie browsershare.Cookie
	value  string
}

func safariShareStatus(profile string) map[string]string {
	operations := map[string]string{"cookie.normalize": "ready", "policy.export": "ready", "cookie.list": "blocked", "cookie.export": "blocked", "cookie.query": "blocked"}
	paths, err := safariCookieStorePaths(profile)
	if err != nil {
		return operations
	}
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return operations
		}
		_ = file.Close()
	}
	operations["cookie.list"] = "ready"
	operations["cookie.export"] = "ready"
	operations["cookie.query"] = "ready"
	return operations
}

func readSafariSiteCookies(profile string, site *url.URL, name string) ([]browsershare.Cookie, string, error) {
	cookies, _, err := querySafariCookies(profile, site, name, false, false)
	return cookies, profile, err
}

func querySafariCookies(profile string, site *url.URL, name string, includeExpired, withValue bool) ([]browsershare.Cookie, string, error) {
	paths, err := safariCookieStorePaths(profile)
	if err != nil {
		return nil, "", err
	}
	var cookies []browsershare.Cookie
	for _, path := range paths {
		records, err := readSafariCookieStore(path, withValue)
		if err != nil {
			return nil, "", err
		}
		for _, record := range records {
			cookie := record.cookie
			if (name == "" || cookie.Name == name) && browsershare.CookieMatchesSiteOptions(site, cookie, includeExpired) {
				if withValue {
					cookie.Value = record.value
				}
				cookies = append(cookies, cookie)
			}
		}
	}
	if len(paths) == 1 {
		return cookies, paths[0], nil
	}
	return cookies, "", nil
}

func readSafariCookieValue(profile string, cookie browsershare.Cookie) (string, error) {
	paths, err := safariCookieStorePaths(profile)
	if err != nil {
		return "", err
	}
	for _, path := range paths {
		records, err := readSafariCookieStore(path, true)
		if err != nil {
			return "", err
		}
		for _, record := range records {
			if browsershare.SameListedCookie(record.cookie, cookie) {
				return record.value, nil
			}
		}
	}
	return "", errors.New("Safari cookie changed since listing; retry")
}

func safariCookieStorePaths(profile string) ([]string, error) {
	if profile != "default" {
		if !filepath.IsAbs(profile) || filepath.Base(profile) != "Cookies.binarycookies" {
			return nil, errors.New("Safari profile must be default or an absolute Cookies.binarycookies path")
		}
		return []string{profile}, nil
	}
	if override := os.Getenv("CTX_SAFARI_COOKIE_FILE"); override != "" {
		if !filepath.IsAbs(override) {
			return nil, errors.New("CTX_SAFARI_COOKIE_FILE must be an absolute path")
		}
		return []string{override}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	candidates := []string{
		filepath.Join(home, "Library", "Containers", "com.apple.Safari", "Data", "Library", "Cookies", "Cookies.binarycookies"),
		filepath.Join(home, "Library", "Cookies", "Cookies.binarycookies"),
	}
	var paths []string
	for _, path := range candidates {
		info, err := os.Stat(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			continue
		case err != nil:
			return nil, fmt.Errorf("access Safari cookie store %s: %w", path, err)
		case !info.Mode().IsRegular():
			return nil, fmt.Errorf("Safari cookie store is not a regular file: %s", path)
		default:
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return nil, errors.New("Safari Cookies.binarycookies was not found at the known profile locations; supply an authorized browser export as inline cookie input")
	}
	return paths, nil
}

func readSafariCookieStore(path string, withValue bool) ([]safariCookieRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open Safari cookie store %s: %w", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSafariCookieStoreSize {
		return nil, fmt.Errorf("Safari cookie store is not a regular file under 128 MiB: %s", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSafariCookieStoreSize+1))
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return nil, errors.New("Safari cookie store changed while reading; retry")
	}
	if len(data) > maxSafariCookieStoreSize || len(data) < 8 || string(data[:4]) != "cook" {
		return nil, fmt.Errorf("unsupported Safari cookie store format: %s", path)
	}
	storeID := fmt.Sprintf("%s\x00%d\x00%d", path, info.Size(), info.ModTime().UnixNano())
	pageCount := binary.BigEndian.Uint32(data[4:8])
	if pageCount == 0 || pageCount > 16384 || uint64(pageCount)*4 > uint64(len(data)-8) {
		return nil, errors.New("invalid Safari cookie page count")
	}
	pageStart := 8 + int(pageCount)*4
	var records []safariCookieRecord
	for page := uint32(0); page < pageCount; page++ {
		size := binary.BigEndian.Uint32(data[8+int(page)*4:])
		if size < 8 || uint64(size) > uint64(len(data)-pageStart) {
			return nil, fmt.Errorf("invalid Safari cookie page %d size", page)
		}
		parsed, err := parseSafariCookiePage(storeID, page, data[pageStart:pageStart+int(size)], withValue)
		if err != nil {
			return nil, err
		}
		records = append(records, parsed...)
		pageStart += int(size)
	}
	return records, nil
}

func parseSafariCookiePage(storeID string, pageNumber uint32, page []byte, withValue bool) ([]safariCookieRecord, error) {
	if len(page) < 8 || !bytes.Equal(page[:4], []byte{0, 0, 1, 0}) {
		return nil, fmt.Errorf("invalid Safari cookie page %d header", pageNumber)
	}
	count := binary.LittleEndian.Uint32(page[4:8])
	if count > 100000 || uint64(count)*4 > uint64(len(page)-8) {
		return nil, fmt.Errorf("invalid Safari cookie count on page %d", pageNumber)
	}
	records := make([]safariCookieRecord, 0, count)
	for index := uint32(0); index < count; index++ {
		start := binary.LittleEndian.Uint32(page[8+int(index)*4:])
		if uint64(start)+56 > uint64(len(page)) {
			return nil, fmt.Errorf("invalid Safari cookie offset on page %d", pageNumber)
		}
		size := binary.LittleEndian.Uint32(page[start:])
		if size < 56 || uint64(size) > uint64(len(page))-uint64(start) {
			return nil, fmt.Errorf("invalid Safari cookie size on page %d", pageNumber)
		}
		record, err := parseSafariCookieRecord(storeID, pageNumber, start, page[start:start+size], withValue)
		if err != nil {
			return nil, fmt.Errorf("Safari cookie page %d entry %d: %w", pageNumber, index, err)
		}
		if record.cookie.Name != "" && record.cookie.Domain != "" {
			records = append(records, record)
		}
	}
	return records, nil
}

func parseSafariCookieRecord(storeID string, pageNumber, offset uint32, data []byte, withValue bool) (safariCookieRecord, error) {
	domain, err := safariCookieString(data, binary.LittleEndian.Uint32(data[16:20]))
	if err != nil {
		return safariCookieRecord{}, err
	}
	name, err := safariCookieString(data, binary.LittleEndian.Uint32(data[20:24]))
	if err != nil {
		return safariCookieRecord{}, err
	}
	cookiePath, err := safariCookieString(data, binary.LittleEndian.Uint32(data[24:28]))
	if err != nil {
		return safariCookieRecord{}, err
	}
	if cookiePath == "" {
		cookiePath = "/"
	}
	if !strings.HasPrefix(cookiePath, "/") {
		return safariCookieRecord{}, errors.New("invalid cookie path")
	}
	expiration := math.Float64frombits(binary.LittleEndian.Uint64(data[40:48]))
	if math.IsNaN(expiration) || math.IsInf(expiration, 0) || expiration < 0 || expiration > 1e12 {
		return safariCookieRecord{}, errors.New("invalid cookie expiration")
	}
	expiry := int64(0)
	if expiration != 0 {
		expiry = safariEpochUnix + int64(expiration)
	}
	flags := binary.LittleEndian.Uint32(data[8:12])
	identity := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d", storeID, pageNumber, offset)))
	ref := hex.EncodeToString(identity[:16])
	record := safariCookieRecord{cookie: browsershare.Cookie{
		Ref: ref, Name: name, Domain: domain, Path: cookiePath, Expiry: expiry,
		Secure: flags&1 != 0, HTTPOnly: flags&4 != 0,
	}}
	if withValue {
		record.value, err = safariCookieString(data, binary.LittleEndian.Uint32(data[28:32]))
		if err != nil {
			return safariCookieRecord{}, err
		}
	}
	return record, nil
}

func safariCookieString(record []byte, offset uint32) (string, error) {
	if offset == 0 || uint64(offset) >= uint64(len(record)) {
		return "", errors.New("invalid cookie string offset")
	}
	end := bytes.IndexByte(record[offset:], 0)
	if end < 0 || !utf8.Valid(record[offset:offset+uint32(end)]) {
		return "", errors.New("invalid cookie string")
	}
	return string(record[offset : offset+uint32(end)]), nil
}
