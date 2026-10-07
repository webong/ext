package firefox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	browsershare "github.com/webong/ext/res/web/contract"
)

type firefoxCookieRow struct {
	ID                   int64  `json:"id"`
	Name                 string `json:"name"`
	Value                string `json:"value"`
	Host                 string `json:"host"`
	Path                 string `json:"path"`
	Expiry               int64  `json:"expiry"`
	IsSecure             int    `json:"isSecure"`
	IsHTTPOnly           int    `json:"isHttpOnly"`
	SameSite             int    `json:"sameSite"`
	OriginAttributes     string `json:"originAttributes"`
	PartitionedAttribute int    `json:"partitionedAttribute"`
}

func readFirefoxSiteCookies(config Config, profile string, site *url.URL, name string) ([]browserCookie, string, error) {
	return queryFirefoxCookies(config, profile, site, name, false)
}

func queryFirefoxCookies(config Config, profile string, site *url.URL, name string, includeExpired bool) ([]browserCookie, string, error) {
	database, err := firefoxCookieDatabase(config, profile)
	if err != nil {
		return nil, "", err
	}
	readableDB, cleanup, columns, err := readableFirefoxCookieDatabase(database)
	if err != nil {
		return nil, "", err
	}
	defer cleanup()
	for _, required := range []string{"id", "name", "value", "host", "path", "expiry", "isSecure", "isHttpOnly", "sameSite", "originAttributes"} {
		if !hasSQLiteColumn(columns, required) {
			return nil, "", fmt.Errorf("Firefox cookie database lacks %s; this profile schema is not supported", required)
		}
	}
	scale, err := firefoxExpiryScale(readableDB)
	if err != nil {
		return nil, "", err
	}
	partitioned := "0"
	if hasSQLiteColumn(columns, "isPartitionedAttributeSet") {
		partitioned = "isPartitionedAttributeSet"
	}
	statement := "SELECT id,name,host,path,expiry,isSecure,isHttpOnly,sameSite,originAttributes," + partitioned + " AS partitionedAttribute FROM moz_cookies WHERE 1=1"
	if site != nil {
		host := strings.TrimSuffix(strings.ToLower(site.Hostname()), ".")
		if host == "" {
			return nil, "", errors.New("site has no hostname")
		}
		statement += " AND host IN (" + cookieHostSQL(host) + ")"
	}
	if name != "" {
		statement += " AND name=" + sqlString(name)
	}
	output, err := runSQLite(readableDB, true, statement)
	if err != nil {
		return nil, "", err
	}
	var rows []firefoxCookieRow
	if len(bytes.TrimSpace(output)) != 0 {
		if err := json.Unmarshal(output, &rows); err != nil {
			return nil, "", errors.New("cannot decode Firefox cookie database response")
		}
	}
	containerNames := firefoxContainerNames(filepath.Dir(database))
	cookies := make([]browserCookie, 0, len(rows))
	for _, row := range rows {
		expiry := row.Expiry / scale
		if expiry <= 0 {
			expiry = -1 // persistent SQLite rows are never session cookies
		}
		cookie := browserCookie{
			ID: row.ID, Name: row.Name, Domain: row.Host, Path: row.Path,
			Expiry: expiry, Secure: row.IsSecure != 0, HTTPOnly: row.IsHTTPOnly != 0,
			SameSitePolicy: firefoxSameSitePolicy(row.SameSite),
		}
		if row.OriginAttributes != "" {
			cookie.Attributes = map[string]string{"firefox.origin_attributes": row.OriginAttributes}
			if id := firefoxContainerID(row.OriginAttributes); id > 0 {
				cookie.Attributes["firefox.container_id"] = strconv.Itoa(id)
				if label := containerNames[id]; label != "" {
					cookie.Attributes["firefox.container_name"] = label
				}
			}
		}
		if row.PartitionedAttribute != 0 {
			if cookie.Attributes == nil {
				cookie.Attributes = map[string]string{}
			}
			cookie.Attributes["firefox.partitioned_attribute"] = "true"
		}
		if browsershare.CookieMatchesSiteOptions(site, cookie, includeExpired) {
			cookies = append(cookies, cookie)
		}
	}
	return cookies, database, nil
}

func firefoxContainerID(originAttributes string) int {
	for _, attribute := range strings.Split(strings.TrimPrefix(originAttributes, "^"), "&") {
		value, ok := strings.CutPrefix(attribute, "userContextId=")
		if !ok {
			continue
		}
		id, err := strconv.Atoi(value)
		if err == nil && id > 0 {
			return id
		}
		return 0
	}
	return 0
}

func firefoxContainerNames(profileDirectory string) map[int]string {
	path := filepath.Join(profileDirectory, "containers.json")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var document struct {
		Identities []struct {
			ID   int    `json:"userContextId"`
			Name string `json:"name"`
		} `json:"identities"`
	}
	if json.Unmarshal(data, &document) != nil {
		return nil
	}
	result := make(map[int]string, len(document.Identities))
	for _, identity := range document.Identities {
		if identity.ID > 0 {
			result[identity.ID] = identity.Name
		}
	}
	return result
}

func readFirefoxCookieValue(database string, cookie browserCookie) (string, error) {
	readableDB, cleanup, _, err := readableFirefoxCookieDatabase(database)
	if err != nil {
		return "", err
	}
	defer cleanup()
	statement := "SELECT value FROM moz_cookies WHERE id=" + strconv.FormatInt(cookie.ID, 10) +
		" AND name=" + sqlString(cookie.Name) + " AND host=" + sqlString(cookie.Domain) +
		" AND path=" + sqlString(cookie.Path) + " AND originAttributes=" + sqlString(cookie.Attributes["firefox.origin_attributes"])
	output, err := runSQLite(readableDB, true, statement)
	if err != nil {
		return "", err
	}
	var rows []struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(output, &rows); err != nil || len(rows) != 1 {
		return "", errors.New("Firefox cookie changed since listing; retry")
	}
	return rows[0].Value, nil
}

func firefoxSameSitePolicy(raw int) string {
	switch raw {
	case 0:
		return "none"
	case 1:
		return "lax"
	case 2:
		return "strict"
	case 256:
		return "unspecified"
	default:
		return "unknown"
	}
}

func cookieDomainMatches(siteHost, cookieDomain string) bool {
	return browsershare.CookieDomainMatches(siteHost, cookieDomain)
}

func firefoxCookieDatabase(config Config, profile string) (string, error) {
	directory, err := firefoxProfileDirectory(config, profile)
	if err != nil {
		return "", err
	}
	database := filepath.Join(directory, "cookies.sqlite")
	info, err := os.Stat(database)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("Firefox cookie database is unavailable for profile %q", profile)
	}
	return database, nil
}

func firefoxProfileDirectory(config Config, profile string) (string, error) {
	if filepath.IsAbs(profile) {
		if filepath.Base(profile) == "cookies.sqlite" {
			return filepath.Dir(profile), nil
		}
		if info, err := os.Stat(profile); err == nil && info.IsDir() {
			return filepath.Clean(profile), nil
		}
		return "", fmt.Errorf("Firefox profile directory is unavailable: %s", profile)
	}
	ini, err := firefoxProfilesINI(config)
	if err != nil {
		return "", err
	}
	file, err := os.Open(ini)
	if err != nil {
		return "", fmt.Errorf("cannot open Firefox profiles.ini: %w", err)
	}
	defer file.Close()
	var matches []string
	section := ""
	fields := map[string]string{}
	flush := func() {
		if !strings.HasPrefix(section, "Profile") || fields["Name"] != profile || fields["Path"] == "" {
			return
		}
		path := filepath.FromSlash(fields["Path"])
		if fields["IsRelative"] != "0" && !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(ini), path)
		}
		matches = append(matches, filepath.Clean(path))
	}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			section = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			fields = map[string]string{}
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	flush()
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("cannot read Firefox profiles.ini: %w", err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("Firefox profile %q is not listed in profiles.ini", profile)
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("Firefox profile name %q is ambiguous", profile)
	}
	return matches[0], nil
}

func firefoxProfilesINI(config Config) (string, error) {
	home := os.Getenv("HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	switch runtime.GOOS {
	case "darwin":
		if config.MacProfileRoot == "" {
			return "", fmt.Errorf("%s has no default macOS profile location", config.Name)
		}
		return filepath.Join(home, "Library", "Application Support", filepath.FromSlash(config.MacProfileRoot), "profiles.ini"), nil
	case "windows":
		if config.WindowsProfileRoot == "" {
			return "", fmt.Errorf("%s has no default Windows profile location", config.Name)
		}
		root := os.Getenv("APPDATA")
		if root == "" {
			return "", errors.New("APPDATA is not set")
		}
		return filepath.Join(root, filepath.FromSlash(config.WindowsProfileRoot), "profiles.ini"), nil
	default:
		if config.LinuxProfileRoot == "" {
			return "", fmt.Errorf("%s has no default Linux profile location", config.Name)
		}
		standard := filepath.Join(home, filepath.FromSlash(config.LinuxProfileRoot), "profiles.ini")
		if _, err := os.Stat(standard); err == nil {
			return standard, nil
		}
		if config.LinuxFallbackProfileRoot != "" {
			return filepath.Join(home, filepath.FromSlash(config.LinuxFallbackProfileRoot), "profiles.ini"), nil
		}
		return standard, nil
	}
}

func firefoxCookieColumns(database string) ([]string, error) {
	return cookieDatabaseColumns(database, "moz_cookies")
}

// Firefox keeps these names for profile-specific callers and its WAL fixture.
func readableFirefoxCookieDatabase(database string) (string, func(), []string, error) {
	return readableCookieDatabase(database, "moz_cookies")
}

func snapshotFirefoxCookieDatabase(database string) (string, func(), error) {
	return snapshotCookieDatabase(database)
}

func importFirefoxCookie(config Config, profile string, cookie browserCookie, replace bool) error {
	if err := browsershare.ValidateCookie(cookie); err != nil {
		return err
	}
	if !cookieActive(cookie) {
		return errors.New("cannot import an expired Firefox cookie")
	}
	if cookie.Expiry == 0 {
		return errors.New("Firefox session cookies cannot be imported into its persistent SQLite store; use a browser-authorized live session")
	}
	if len(cookie.Attributes) != 0 || cookie.PartitionKey != "" || cookie.CrossSiteAncestor {
		return errors.New("Firefox profile import cannot map a container or partitioned cookie")
	}
	database, err := firefoxCookieDatabase(config, profile)
	if err != nil {
		return err
	}
	if err := ensureFirefoxProfileClosed(database); err != nil {
		return err
	}
	columns, err := firefoxCookieColumns(database)
	if err != nil {
		return err
	}
	for _, required := range []string{"name", "value", "host", "path", "expiry", "isSecure", "isHttpOnly", "sameSite", "originAttributes"} {
		if !hasSQLiteColumn(columns, required) {
			return fmt.Errorf("Firefox target cookie database lacks %s", required)
		}
	}
	sameSite, err := firefoxImportedSameSite(cookie)
	if err != nil {
		return err
	}
	scale, err := firefoxExpiryScale(database)
	if err != nil {
		return err
	}
	identity := "name=" + sqlString(cookie.Name) + " AND host=" + sqlString(cookie.Domain) + " AND path=" + sqlString(cookie.Path) + " AND originAttributes=''"
	if !replace {
		output, err := runSQLite(database, true, "SELECT count(*) AS existing FROM moz_cookies WHERE "+identity)
		if err != nil {
			return err
		}
		var counts []struct {
			Existing int `json:"existing"`
		}
		if err := json.Unmarshal(output, &counts); err != nil || len(counts) != 1 {
			return errors.New("cannot check destination Firefox cookie")
		}
		if counts[0].Existing != 0 {
			return errors.New("destination already has this cookie; use --replace to overwrite it")
		}
	}
	values := map[string]string{
		"name": sqlString(cookie.Name), "value": sqlString(cookie.Value),
		"host": sqlString(cookie.Domain), "path": sqlString(cookie.Path),
		"expiry": strconv.FormatInt(cookie.Expiry*scale, 10), "isSecure": sqlBool(cookie.Secure),
		"isHttpOnly": sqlBool(cookie.HTTPOnly), "sameSite": strconv.Itoa(sameSite),
		"originAttributes": "''",
		"creationTime":     strconv.FormatInt(time.Now().UnixMicro(), 10),
		"lastAccessed":     strconv.FormatInt(time.Now().UnixMicro(), 10),
		"updateTime":       strconv.FormatInt(time.Now().UnixMicro(), 10),
	}
	var names, expressions []string
	for _, column := range columns {
		if value, ok := values[column]; ok {
			names = append(names, sqlIdentifier(column))
			expressions = append(expressions, value)
		}
	}
	statement := "BEGIN IMMEDIATE; "
	if replace {
		statement += "DELETE FROM moz_cookies WHERE " + identity + "; "
	}
	statement += "INSERT INTO moz_cookies (" + strings.Join(names, ",") + ") VALUES (" + strings.Join(expressions, ",") + "); COMMIT;"
	_, err = runSQLite(database, false, statement)
	return err
}

// Firefox schema 16 migrated expiry from seconds to milliseconds. Keep the
// portable contract in seconds, and avoid guessing about future migrations.
func firefoxExpiryScale(database string) (int64, error) {
	output, err := runSQLite(database, true, "PRAGMA user_version")
	if err != nil {
		return 0, err
	}
	var versions []struct {
		Version int `json:"user_version"`
	}
	if json.Unmarshal(output, &versions) != nil || len(versions) != 1 || versions[0].Version < 0 || versions[0].Version > 17 {
		return 0, errors.New("unsupported Firefox cookie schema version")
	}
	if versions[0].Version >= 16 {
		return 1000, nil
	}
	return 1, nil
}

func firefoxImportedSameSite(cookie browserCookie) (int, error) {
	switch cookie.SameSitePolicy {
	case "none":
		return 0, nil
	case "lax":
		return 1, nil
	case "strict":
		return 2, nil
	case "unspecified", "":
		return 256, nil
	default:
		return 0, errors.New("unsupported cookie SameSite policy")
	}
}

func ensureFirefoxProfileClosed(database string) error {
	if _, err := exec.LookPath("lsof"); err != nil {
		return errors.New("lsof is required to check Firefox profile locks before copying")
	}
	profileDir := filepath.Dir(database)
	for _, path := range []string{database, filepath.Join(profileDir, ".parentlock"), filepath.Join(profileDir, "parent.lock"), filepath.Join(profileDir, "lock")} {
		if _, err := os.Lstat(path); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		command := exec.CommandContext(ctx, "lsof", "-t", path)
		output, err := command.Output()
		cancel()
		if err == nil && len(bytes.TrimSpace(output)) != 0 {
			return fmt.Errorf("Firefox profile %s appears to be open; close it before copying cookies", profileDir)
		}
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
				continue
			}
			return fmt.Errorf("cannot check Firefox profile lock in %s", profileDir)
		}
	}
	return nil
}
