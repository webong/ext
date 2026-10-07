package chromium

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	"unicode/utf8"

	credentialclient "github.com/webong/ext/res/credential/client"
	share "github.com/webong/ext/res/web/contract"
)

const chromiumEpochOffsetMicros = int64(11644473600000000)

type chromiumCookieRow struct {
	ID                   int64  `json:"id"`
	Name                 string `json:"name"`
	Host                 string `json:"host"`
	Path                 string `json:"path"`
	ExpiresUTC           int64  `json:"expiresUTC"`
	IsSecure             int    `json:"isSecure"`
	IsHTTPOnly           int    `json:"isHttpOnly"`
	SameSite             int    `json:"sameSite"`
	TopFrameSiteKey      string `json:"topFrameSiteKey"`
	HasCrossSiteAncestor int    `json:"hasCrossSiteAncestor"`
	HasExpires           int    `json:"hasExpires"`
}

func readChromiumSiteCookies(provider Config, profile string, site *url.URL, name string) ([]browserCookie, string, error) {
	return queryChromiumCookies(provider, profile, site, name, false)
}

func queryChromiumCookies(provider Config, profile string, site *url.URL, name string, includeExpired bool) ([]browserCookie, string, error) {
	database, err := chromiumCookieDatabase(provider, profile)
	if err != nil {
		return nil, "", err
	}
	readable, cleanup, columns, err := readableCookieDatabase(database, "cookies")
	if err != nil {
		return nil, "", err
	}
	defer cleanup()
	for _, required := range []string{"host_key", "name", "path", "expires_utc", "is_secure", "is_httponly", "samesite", "value", "encrypted_value"} {
		if !hasSQLiteColumn(columns, required) {
			return nil, "", fmt.Errorf("%s cookie database lacks %s; this profile schema is not supported", provider.Name, required)
		}
	}
	statement := "SELECT rowid AS id,name,host_key AS host,path,expires_utc AS expiresUTC," +
		"is_secure AS isSecure,is_httponly AS isHttpOnly,samesite AS sameSite," +
		chromiumColumnExpr(columns, "top_frame_site_key", "''", "topFrameSiteKey") + "," +
		chromiumColumnExpr(columns, "has_cross_site_ancestor", "0", "hasCrossSiteAncestor") + "," +
		chromiumColumnExpr(columns, "has_expires", "1", "hasExpires") +
		" FROM cookies WHERE 1=1"
	if site != nil {
		host := strings.TrimSuffix(strings.ToLower(site.Hostname()), ".")
		if host == "" {
			return nil, "", errors.New("site has no hostname")
		}
		statement += " AND host_key IN (" + cookieHostSQL(host) + ")"
	}
	if name != "" {
		statement += " AND name=" + sqlString(name)
	}
	output, err := runSQLite(readable, true, statement)
	if err != nil {
		return nil, "", err
	}
	var rows []chromiumCookieRow
	if len(bytes.TrimSpace(output)) != 0 {
		if err := json.Unmarshal(output, &rows); err != nil {
			return nil, "", errors.New("cannot decode Chromium cookie database response")
		}
	}
	cookies := make([]browserCookie, 0, len(rows))
	for _, row := range rows {
		expiry := int64(0)
		if row.HasExpires != 0 {
			if row.ExpiresUTC <= chromiumEpochOffsetMicros {
				expiry = -1
			} else {
				expiry = (row.ExpiresUTC - chromiumEpochOffsetMicros) / 1_000_000
			}
		}
		cookie := browserCookie{
			ID: row.ID, Name: row.Name, Domain: row.Host, Path: row.Path,
			Expiry: expiry, Secure: row.IsSecure != 0, HTTPOnly: row.IsHTTPOnly != 0,
			SameSitePolicy: chromiumSameSitePolicy(row.SameSite),
			PartitionKey:   row.TopFrameSiteKey, CrossSiteAncestor: row.HasCrossSiteAncestor != 0,
		}
		if share.CookieMatchesSiteOptions(site, cookie, includeExpired) {
			cookies = append(cookies, cookie)
		}
	}
	return cookies, database, nil
}

func chromiumColumnExpr(columns []string, column, fallback, alias string) string {
	if hasSQLiteColumn(columns, column) {
		return sqlIdentifier(column) + " AS " + sqlIdentifier(alias)
	}
	return fallback + " AS " + sqlIdentifier(alias)
}

func chromiumSameSitePolicy(raw int) string {
	switch raw {
	case -1:
		return "unspecified"
	case 0:
		return "none"
	case 1:
		return "lax"
	case 2:
		return "strict"
	default:
		return "unknown"
	}
}

func chromiumCookieDatabase(provider Config, profile string) (string, error) {
	if filepath.IsAbs(profile) {
		if info, err := os.Stat(profile); err == nil && info.Mode().IsRegular() && filepath.Base(profile) == "Cookies" {
			return profile, nil
		}
		if info, err := os.Stat(filepath.Join(profile, "Preferences")); err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("%s profile directory is unavailable: %s", provider.Name, profile)
		}
		return chromiumCookieDatabaseInProfile(provider.Name, profile)
	}
	if profile == "" || profile == "." || profile == ".." || filepath.Base(profile) != profile || strings.ContainsAny(profile, `/\\`) {
		return "", errors.New("invalid Chromium profile directory")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	var root string
	switch runtime.GOOS {
	case "darwin":
		if provider.MacUserData == "" {
			return "", fmt.Errorf("%s has no default macOS profile location", provider.Name)
		}
		root = filepath.Join(home, "Library", "Application Support", provider.MacUserData)
	case "windows":
		if provider.WindowsUserData == "" {
			return "", fmt.Errorf("%s has no default Windows profile location", provider.Name)
		}
		environment := "LOCALAPPDATA"
		if provider.WindowsRoaming {
			environment = "APPDATA"
		}
		local := os.Getenv(environment)
		if local == "" {
			return "", fmt.Errorf("%s is not set", environment)
		}
		root = filepath.Join(local, provider.WindowsUserData)
	default:
		if provider.LinuxUserData == "" {
			return "", fmt.Errorf("%s has no default Linux profile location", provider.Name)
		}
		root = os.Getenv("XDG_CONFIG_HOME")
		if root == "" {
			root = filepath.Join(home, ".config")
		}
		root = filepath.Join(root, provider.LinuxUserData)
	}
	profileDir := filepath.Join(root, profile)
	if profile == "root" {
		profileDir = root
	}
	if info, err := os.Stat(filepath.Join(profileDir, "Preferences")); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s profile %q is unavailable", provider.Name, profile)
	}
	return chromiumCookieDatabaseInProfile(provider.Name, profileDir)
}

func chromiumCookieDatabaseInProfile(name, profileDir string) (string, error) {
	for _, candidate := range []string{filepath.Join(profileDir, "Network", "Cookies"), filepath.Join(profileDir, "Cookies")} {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s cookie database is unavailable for profile %q", name, profileDir)
}

func readChromiumCookieValue(provider Config, database string, cookie browserCookie) (string, error) {
	return readChromiumCookieValueWithDecryptor(database, cookie, newChromiumCookieDecryptor(provider).decrypt)
}

func readChromiumCookieValueWithDecryptor(database string, cookie browserCookie, decrypt func(string, []byte) (string, error)) (string, error) {
	readable, cleanup, columns, err := readableCookieDatabase(database, "cookies")
	if err != nil {
		return "", err
	}
	defer cleanup()
	statement := "SELECT value,hex(encrypted_value) AS encryptedHex FROM cookies WHERE rowid=" + strconv.FormatInt(cookie.ID, 10) +
		" AND name=" + sqlString(cookie.Name) + " AND host_key=" + sqlString(cookie.Domain) + " AND path=" + sqlString(cookie.Path)
	if hasSQLiteColumn(columns, "top_frame_site_key") {
		statement += " AND top_frame_site_key=" + sqlString(cookie.PartitionKey)
	}
	if hasSQLiteColumn(columns, "has_cross_site_ancestor") {
		ancestor := 0
		if cookie.CrossSiteAncestor {
			ancestor = 1
		}
		statement += " AND has_cross_site_ancestor=" + strconv.Itoa(ancestor)
	}
	output, err := runSQLite(readable, true, statement)
	if err != nil {
		return "", err
	}
	var rows []struct {
		Value        string `json:"value"`
		EncryptedHex string `json:"encryptedHex"`
	}
	if err := json.Unmarshal(output, &rows); err != nil || len(rows) != 1 {
		return "", errors.New("Chromium cookie changed since listing; retry")
	}
	if rows[0].Value != "" && rows[0].EncryptedHex != "" {
		return "", errors.New("Chromium cookie contains both plaintext and encrypted values")
	}
	if rows[0].EncryptedHex == "" {
		return rows[0].Value, nil
	}
	ciphertext, err := hex.DecodeString(rows[0].EncryptedHex)
	if err != nil {
		return "", errors.New("Chromium cookie ciphertext is malformed")
	}
	plaintext, err := decrypt(database, ciphertext)
	if err != nil {
		return "", err
	}
	versionOutput, err := runSQLite(readable, true, "SELECT value AS version FROM meta WHERE key='version'")
	if err != nil {
		return "", fmt.Errorf("cannot determine Chromium cookie database version: %w", err)
	}
	var versions []struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(versionOutput, &versions); err != nil || len(versions) != 1 {
		return "", errors.New("cannot determine Chromium cookie database version")
	}
	if versions[0].Version >= 24 {
		// Chromium database v24 binds the encrypted value to its host key.
		// See net/extras/sqlite/sqlite_persistent_cookie_store.cc.
		hostHash := sha256.Sum256([]byte(cookie.Domain))
		if len(plaintext) < len(hostHash) || !bytes.Equal([]byte(plaintext[:len(hostHash)]), hostHash[:]) {
			return "", errors.New("Chromium cookie domain hash does not match; cookie was not exported")
		}
		plaintext = plaintext[len(hostHash):]
	}
	if !utf8.ValidString(plaintext) {
		return "", errors.New("Chromium cookie value is not valid UTF-8")
	}
	return plaintext, nil
}

func decryptChromiumCookie(provider Config, database string, ciphertext []byte) (string, error) {
	return newChromiumCookieDecryptor(provider).decrypt(database, ciphertext)
}

// Keys and credential failures are cached for one adapter invocation only.
// A query must not prompt once per cookie or repeatedly retry denied access.
type chromiumCookieDecryptor struct {
	provider Config
	keys     map[string]chromiumCookieKey
}

type chromiumCookieKey struct {
	key []byte
	err error
}

func newChromiumCookieDecryptor(provider Config) *chromiumCookieDecryptor {
	return &chromiumCookieDecryptor{provider: provider, keys: make(map[string]chromiumCookieKey)}
}

func (decryptor *chromiumCookieDecryptor) key(identity string, load func() ([]byte, error)) ([]byte, error) {
	if result, ok := decryptor.keys[identity]; ok {
		return result.key, result.err
	}
	key, err := load()
	decryptor.keys[identity] = chromiumCookieKey{key: key, err: err}
	return key, err
}

func (decryptor *chromiumCookieDecryptor) decrypt(database string, ciphertext []byte) (string, error) {
	if len(ciphertext) < 3 {
		return "", errors.New("Chromium cookie uses an unsupported encryption format")
	}
	version := string(ciphertext[:3])
	if runtime.GOOS == "windows" {
		return decryptor.windows(database, ciphertext)
	}
	if len(ciphertext)-3 == 0 || (len(ciphertext)-3)%aes.BlockSize != 0 {
		return "", errors.New("Chromium cookie ciphertext has an invalid length")
	}
	var key []byte
	var err error
	switch runtime.GOOS {
	case "darwin":
		if version != "v10" {
			return "", errors.New("Chromium cookie encryption format is not supported on macOS")
		}
		key, err = decryptor.key("macos", func() ([]byte, error) {
			secret, err := chromiumMacKeychainPassword(decryptor.provider)
			if err != nil {
				return nil, err
			}
			return chromiumPBKDF2Key([]byte(secret), 1003), nil
		})
	case "linux":
		switch version {
		case "v10":
			key = chromiumPBKDF2Key([]byte("peanuts"), 1)
		case "v11":
			key, err = decryptor.key("linux", func() ([]byte, error) {
				secret, err := chromiumLinuxSecret(decryptor.provider)
				if err != nil {
					return nil, err
				}
				return chromiumPBKDF2Key([]byte(secret), 1), nil
			})
		default:
			return "", errors.New("Chromium cookie encryption format is not supported on Linux")
		}
	default:
		return "", fmt.Errorf("encrypted Chromium cookie export is not supported on %s", runtime.GOOS)
	}
	if err != nil {
		return "", err
	}
	return decryptChromiumCBC(key, ciphertext[3:])
}

func decryptChromiumCBC(key, data []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", errors.New("cannot initialize Chromium cookie decryptor")
	}
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return "", errors.New("Chromium cookie ciphertext has an invalid length")
	}
	plaintext := make([]byte, len(data))
	iv := bytes.Repeat([]byte(" "), aes.BlockSize)
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, data)
	padding := int(plaintext[len(plaintext)-1])
	if padding == 0 || padding > aes.BlockSize || padding > len(plaintext) {
		return "", errors.New("Chromium cookie decryption failed")
	}
	for _, value := range plaintext[len(plaintext)-padding:] {
		if int(value) != padding {
			return "", errors.New("Chromium cookie decryption failed")
		}
	}
	return string(plaintext[:len(plaintext)-padding]), nil
}

func decryptChromiumWindowsCookie(database string, ciphertext []byte) (string, error) {
	return newChromiumCookieDecryptor(Config{}).windows(database, ciphertext)
}

func (decryptor *chromiumCookieDecryptor) windows(database string, ciphertext []byte) (string, error) {
	if bytes.HasPrefix(ciphertext, []byte("v20")) {
		return "", errors.New("Chromium cookie uses Windows App-Bound Encryption; standalone profile export is unavailable; supply an authorized browser export as inline cookie input")
	}
	if !bytes.HasPrefix(ciphertext, []byte("v10")) && !bytes.HasPrefix(ciphertext, []byte("v11")) {
		plaintext, err := windowsDPAPIUnprotect(ciphertext)
		return string(plaintext), err
	}
	data := ciphertext[3:]
	if len(data) < 12+16 {
		return "", errors.New("Chromium cookie ciphertext is too short")
	}
	key, err := decryptor.key(chromiumUserDataRootFromDatabase(database), func() ([]byte, error) {
		return chromiumWindowsLegacyKey(database)
	})
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plaintext, err := gcm.Open(nil, data[:12], data[12:], nil)
	if err != nil {
		return "", errors.New("Chromium cookie decryption failed")
	}
	return string(plaintext), nil
}

func chromiumWindowsLegacyKey(database string) ([]byte, error) {
	root := chromiumUserDataRootFromDatabase(database)
	content, err := os.ReadFile(filepath.Join(root, "Local State"))
	if err != nil {
		return nil, fmt.Errorf("cannot read Chromium Local State: %w", err)
	}
	var state struct {
		OSCrypt struct {
			EncryptedKey string `json:"encrypted_key"`
		} `json:"os_crypt"`
	}
	if err := json.Unmarshal(content, &state); err != nil || state.OSCrypt.EncryptedKey == "" {
		return nil, errors.New("Chromium Local State has no legacy encrypted key")
	}
	encoded, err := base64.StdEncoding.DecodeString(state.OSCrypt.EncryptedKey)
	if err != nil || !bytes.HasPrefix(encoded, []byte("DPAPI")) {
		return nil, errors.New("Chromium Local State uses an unsupported key format")
	}
	return windowsDPAPIUnprotect(encoded[5:])
}

func chromiumUserDataRootFromDatabase(database string) string {
	profileDir := filepath.Dir(database)
	if filepath.Base(profileDir) == "Network" {
		profileDir = filepath.Dir(profileDir)
	}
	if info, err := os.Stat(filepath.Join(profileDir, "Local State")); err == nil && info.Mode().IsRegular() {
		return profileDir
	}
	return filepath.Dir(profileDir)
}

func windowsDPAPIUnprotect(ciphertext []byte) ([]byte, error) {
	const script = `Add-Type -AssemblyName System.Security; $raw=[Convert]::FromBase64String($env:CTX_DPAPI_DATA); $clear=[Security.Cryptography.ProtectedData]::Unprotect($raw,$null,[Security.Cryptography.DataProtectionScope]::CurrentUser); [Console]::Out.Write([Convert]::ToBase64String($clear))`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	command.Env = append(os.Environ(), "CTX_DPAPI_DATA="+base64.StdEncoding.EncodeToString(ciphertext))
	output, err := command.Output()
	if err != nil {
		return nil, errors.New("Windows DPAPI could not unlock this Chromium cookie for the current user")
	}
	plaintext, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(output)))
	if err != nil {
		return nil, errors.New("Windows DPAPI returned malformed data")
	}
	return plaintext, nil
}

func chromiumPBKDF2Key(password []byte, iterations int) []byte {
	// Chromium's desktop OSCrypt v10/v11 key derivation uses PBKDF2-HMAC-SHA1
	// with saltysalt; macOS uses 1003 rounds and Linux uses one.
	mac := hmac.New(sha1.New, password)
	_, _ = mac.Write([]byte("saltysalt\x00\x00\x00\x01"))
	previous := mac.Sum(nil)
	derived := append([]byte(nil), previous...)
	for i := 1; i < iterations; i++ {
		mac.Reset()
		_, _ = mac.Write(previous)
		previous = mac.Sum(nil)
		for j := range derived {
			derived[j] ^= previous[j]
		}
	}
	return derived[:16]
}

func chromiumMacKeychainPassword(provider Config) (string, error) {
	if password, set, err := configuredSafeStoragePassword(provider); set || err != nil {
		return password, err
	}
	service, account := provider.KeychainService, provider.KeychainAccount
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	item := "service=" + url.QueryEscape(service) + "&account=" + url.QueryEscape(account)
	extra := map[string]string{}
	if provider.KeychainPath != "" {
		if !filepath.IsAbs(provider.KeychainPath) {
			return "", errors.New("macOS keychain path must be absolute")
		}
		extra["CTX_KEYCHAIN_PATH"] = provider.KeychainPath
	}
	value, err := credentialclient.GetDeclaredWithEnv(ctx, "credential", item, extra)
	if err != nil {
		return "", fmt.Errorf("cannot read %s from macOS Keychain: %w", service, err)
	}
	return string(value), nil
}

func chromiumLinuxSecret(provider Config) (string, error) {
	if password, set, err := configuredSafeStoragePassword(provider); set || err != nil {
		return password, err
	}
	var secretErr, walletErr error
	if strings.Contains(strings.ToUpper(os.Getenv("XDG_CURRENT_DESKTOP")), "KDE") {
		if secret, err := chromiumKWalletSecret(provider); err == nil {
			return secret, nil
		} else {
			walletErr = err
		}
		if secret, err := chromiumSecretServiceSecret(provider); err == nil {
			return secret, nil
		} else {
			secretErr = err
		}
	} else {
		if secret, err := chromiumSecretServiceSecret(provider); err == nil {
			return secret, nil
		} else {
			secretErr = err
		}
		if secret, err := chromiumKWalletSecret(provider); err == nil {
			return secret, nil
		} else {
			walletErr = err
		}
	}
	return "", fmt.Errorf("cannot read the %s cookie key (Secret Service: %v; KWallet: %v)", provider.Name, secretErr, walletErr)
}

func chromiumSecretServiceSecret(provider Config) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	value, primaryErr := credentialclient.GetDeclared(ctx, "credential", "application="+url.QueryEscape(provider.SecretApplication))
	if primaryErr == nil {
		return strings.TrimRight(string(value), "\r\n"), nil
	}
	// Some stores use service/account attributes rather than Chromium's
	// application attribute. Both identities are owned by this adapter.
	if provider.KeychainService != "" && provider.KeychainAccount != "" {
		item := "service=" + url.QueryEscape(provider.KeychainService) + "&account=" + url.QueryEscape(provider.KeychainAccount)
		value, err := credentialclient.GetDeclared(ctx, "credential", item)
		if err == nil {
			return strings.TrimRight(string(value), "\r\n"), nil
		}
		return "", fmt.Errorf("Secret Service key unavailable (application: %v; service/account: %v)", primaryErr, err)
	}
	return "", fmt.Errorf("Secret Service key unavailable: %w", primaryErr)
}

func chromiumKWalletSecret(provider Config) (string, error) {
	if _, err := exec.LookPath("kwallet-query"); err == nil {
		folder, key := provider.WalletFolder, provider.WalletKey
		wallet := os.Getenv("CTX_KWALLET_NAME")
		if wallet == "" {
			wallet = chromiumNetworkWallet()
		}
		if strings.ContainsAny(wallet, "\r\n\x00") || strings.HasPrefix(wallet, "-") {
			return "", errors.New("invalid KWallet name")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		output, lookupErr := exec.CommandContext(ctx, "kwallet-query", "-f", folder, "-r", key, wallet).Output()
		cancel()
		value := strings.TrimRight(string(output), "\r\n")
		if lookupErr == nil && value != "" && !strings.HasPrefix(strings.ToLower(value), "failed to read") {
			return value, nil
		}
	}
	return "", errors.New("KWallet key unavailable")
}

func chromiumNetworkWallet() string {
	if _, err := exec.LookPath("dbus-send"); err != nil {
		return "kdewallet"
	}
	versions := []string{"6", "5", ""}
	if version := os.Getenv("KDE_SESSION_VERSION"); version == "5" {
		versions = []string{"5", "6", ""}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, version := range versions {
		output, err := exec.CommandContext(ctx, "dbus-send", "--session", "--print-reply=literal", "--reply-timeout=1000",
			"--dest=org.kde.kwalletd"+version, "/modules/kwalletd"+version,
			"org.kde.KWallet.networkWallet").Output()
		wallet := strings.TrimSpace(string(output))
		if err == nil && wallet != "" && !strings.ContainsAny(wallet, "\r\n\x00\"") && !strings.HasPrefix(wallet, "-") {
			return wallet
		}
	}
	return "kdewallet"
}
