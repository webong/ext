package chromium

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	share "github.com/webong/ext/res/web/contract"
)

func importChromiumCookie(provider Config, profile string, cookie browserCookie, replace bool) error {
	if err := share.ValidateCookie(cookie); err != nil {
		return err
	}
	if !cookieActive(cookie) {
		return errors.New("cannot import an expired Chromium cookie")
	}
	if runtime.GOOS == "windows" {
		return errors.New("Chrome/Chromium profile import is unavailable on Windows because target encryption is browser-bound")
	}
	if len(cookie.Attributes) != 0 {
		return errors.New("Chromium profile import cannot map this adapter's cookie attributes")
	}
	database, err := chromiumCookieDatabase(provider, profile)
	if err != nil {
		return err
	}
	if err := ensureChromiumProfileClosed(database); err != nil {
		return err
	}
	columns, err := cookieDatabaseColumns(database, "cookies")
	if err != nil {
		return err
	}
	for _, required := range []string{"host_key", "name", "path", "value", "encrypted_value", "expires_utc", "is_secure", "is_httponly", "samesite"} {
		if !hasSQLiteColumn(columns, required) {
			return fmt.Errorf("%s target cookie database lacks %s", provider.Name, required)
		}
	}
	if cookie.PartitionKey != "" && !hasSQLiteColumn(columns, "top_frame_site_key") {
		return errors.New("target Chromium profile cannot represent this partitioned cookie")
	}
	if cookie.CrossSiteAncestor && !hasSQLiteColumn(columns, "has_cross_site_ancestor") {
		return errors.New("target Chromium profile cannot represent the cookie partition ancestor")
	}
	sameSite, err := chromiumImportedSameSite(cookie)
	if err != nil {
		return err
	}
	dbVersion, err := chromiumDatabaseVersion(database)
	if err != nil {
		return err
	}
	version, password, iterations, err := chromiumTargetEncryption(provider, database)
	if err != nil {
		return err
	}
	plaintext := []byte(cookie.Value)
	if dbVersion >= 24 {
		hash := sha256.Sum256([]byte(cookie.Domain))
		plaintext = append(hash[:], plaintext...)
	}
	key := chromiumPBKDF2Key([]byte(password), iterations)
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	padding := aes.BlockSize - len(plaintext)%aes.BlockSize
	plaintext = append(plaintext, bytes.Repeat([]byte{byte(padding)}, padding)...)
	encrypted := make([]byte, len(plaintext))
	cipher.NewCBCEncrypter(block, bytes.Repeat([]byte(" "), aes.BlockSize)).CryptBlocks(encrypted, plaintext)
	encrypted = append([]byte(version), encrypted...)
	expiry := int64(0)
	persistent := cookie.Expiry > 0
	if persistent {
		expiry = cookie.Expiry*1_000_000 + chromiumEpochOffsetMicros
	}
	identity := "host_key=" + sqlString(cookie.Domain) + " AND name=" + sqlString(cookie.Name) + " AND path=" + sqlString(cookie.Path)
	if hasSQLiteColumn(columns, "top_frame_site_key") {
		identity += " AND top_frame_site_key=" + sqlString(cookie.PartitionKey)
	}
	if hasSQLiteColumn(columns, "has_cross_site_ancestor") {
		identity += " AND has_cross_site_ancestor=" + sqlBool(cookie.CrossSiteAncestor)
	}
	if !replace {
		output, err := runSQLite(database, true, "SELECT count(*) AS existing FROM cookies WHERE "+identity)
		if err != nil {
			return err
		}
		var counts []struct {
			Existing int `json:"existing"`
		}
		if err := json.Unmarshal(output, &counts); err != nil || len(counts) != 1 {
			return errors.New("cannot check destination Chromium cookie")
		}
		if counts[0].Existing != 0 {
			return errors.New("destination already has this cookie; use --replace to overwrite it")
		}
	}
	now := time.Now().UnixMicro() + chromiumEpochOffsetMicros
	values := map[string]string{
		"host_key": sqlString(cookie.Domain), "name": sqlString(cookie.Name),
		"path": sqlString(cookie.Path), "value": "''",
		"encrypted_value": "X'" + hex.EncodeToString(encrypted) + "'",
		"creation_utc":    strconv.FormatInt(now, 10),
		"last_access_utc": strconv.FormatInt(now, 10),
		"last_update_utc": strconv.FormatInt(now, 10),
		"expires_utc":     strconv.FormatInt(expiry, 10),
		"is_secure":       sqlBool(cookie.Secure), "is_httponly": sqlBool(cookie.HTTPOnly),
		"samesite": strconv.Itoa(sameSite), "has_expires": sqlBool(persistent),
		"is_persistent": sqlBool(persistent), "priority": "1", "source_scheme": "0",
		"source_port": "-1", "source_type": "0", "is_same_party": "0", "top_frame_site_key": sqlString(cookie.PartitionKey),
		"has_cross_site_ancestor": sqlBool(cookie.CrossSiteAncestor),
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
		statement += "DELETE FROM cookies WHERE " + identity + "; "
	}
	statement += "INSERT INTO cookies (" + strings.Join(names, ",") + ") VALUES (" + strings.Join(expressions, ",") + "); COMMIT;"
	_, err = runSQLite(database, false, statement)
	return err
}

func chromiumImportedSameSite(cookie browserCookie) (int, error) {
	switch cookie.SameSitePolicy {
	case "none":
		return 0, nil
	case "lax":
		return 1, nil
	case "strict":
		return 2, nil
	case "unspecified", "":
		return -1, nil
	default:
		return 0, errors.New("unsupported cookie SameSite policy")
	}
}

func chromiumDatabaseVersion(database string) (int, error) {
	output, err := runSQLite(database, true, "SELECT value AS version FROM meta WHERE key='version'")
	if err != nil {
		return 0, err
	}
	var versions []struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(output, &versions); err != nil || len(versions) != 1 {
		return 0, errors.New("cannot determine Chromium cookie database version")
	}
	return versions[0].Version, nil
}

func chromiumTargetEncryption(provider Config, database string) (string, string, int, error) {
	if runtime.GOOS == "darwin" {
		secret, err := chromiumMacKeychainPassword(provider)
		return "v10", secret, 1003, err
	}
	output, err := runSQLite(database, true, "SELECT hex(substr(encrypted_value,1,3)) AS prefix FROM cookies WHERE length(encrypted_value)>3 LIMIT 1")
	if err != nil {
		return "", "", 0, err
	}
	var rows []struct {
		Prefix string `json:"prefix"`
	}
	if err := json.Unmarshal(output, &rows); err != nil {
		return "", "", 0, errors.New("cannot determine Chromium target encryption format")
	}
	if len(rows) == 1 && strings.EqualFold(rows[0].Prefix, hex.EncodeToString([]byte("v10"))) {
		return "v10", "peanuts", 1, nil
	}
	if len(rows) == 1 && !strings.EqualFold(rows[0].Prefix, hex.EncodeToString([]byte("v11"))) {
		return "", "", 0, errors.New("Chromium target uses an unsupported encryption format")
	}
	secret, err := chromiumLinuxSecret(provider)
	if err != nil {
		return "", "", 0, err
	}
	return "v11", secret, 1, nil
}

func ensureChromiumProfileClosed(database string) error {
	if _, err := os.Lstat(filepath.Join(chromiumUserDataRootFromDatabase(database), "SingletonLock")); err == nil {
		return errors.New("Chromium user data directory has a SingletonLock; close the browser before importing cookies")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot check Chromium SingletonLock: %w", err)
	}
	if _, err := exec.LookPath("lsof"); err != nil {
		return errors.New("lsof is required to check Chromium profile locks before importing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "lsof", "-t", database).Output()
	if err == nil && len(bytes.TrimSpace(output)) > 0 {
		return errors.New("Chromium cookie database is open; close the browser before importing")
	}
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			return nil
		}
		return fmt.Errorf("cannot check Chromium profile lock: %w", err)
	}
	return nil
}
