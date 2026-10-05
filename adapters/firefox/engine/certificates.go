package firefox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	share "github.com/webong/ctx/res/browser/contract"
)

type firefoxCertificatePayload struct {
	Format   string `json:"format"`
	Nickname string `json:"nickname,omitempty"`
	Data     string `json:"data"`
}

func nativeFirefoxCertificateCommand(config Config, profile, operation string, input io.Reader, stdout, stderr io.Writer) int {
	if operation != "list" && operation != "export" && operation != "import" {
		fmt.Fprintln(stderr, "ctx: Firefox certificate operation must be list, export, or import")
		return 2
	}
	var request browserResourceRequest
	if err := json.NewDecoder(io.LimitReader(input, 16<<20)).Decode(&request); err != nil || request.Version != share.Version {
		fmt.Fprintln(stderr, "ctx: invalid certificate share request")
		return 2
	}
	flags := flag.NewFlagSet("certificate "+operation, flag.ContinueOnError)
	flags.SetOutput(stderr)
	nickname := flags.String("nickname", "", "certificate nickname")
	passwordFile := flags.String("password-file", "", "PKCS#12 password file")
	slotPasswordFile := flags.String("slot-password-file", "", "NSS slot password file")
	if err := flags.Parse(request.Args); err != nil || len(flags.Args()) != 0 {
		return 2
	}
	if operation == "export" && *nickname == "" {
		fmt.Fprintln(stderr, "ctx: certificate export needs --nickname")
		return 2
	}
	if operation != "list" && *passwordFile == "" {
		fmt.Fprintln(stderr, "ctx: certificate export/import needs --password-file")
		return 2
	}
	if operation == "list" && (*nickname != "" || *passwordFile != "" || *slotPasswordFile != "") {
		fmt.Fprintln(stderr, "ctx: certificate list takes no adapter arguments")
		return 2
	}
	if *passwordFile != "" {
		if err := validateCertificatePasswordFile(*passwordFile); err != nil {
			return reportErrorCode(stderr, err, 2)
		}
	}
	if *slotPasswordFile != "" {
		if err := validateCertificatePasswordFile(*slotPasswordFile); err != nil {
			return reportErrorCode(stderr, err, 2)
		}
	}
	directory, err := firefoxProfileDirectory(config, profile)
	if err != nil {
		return reportError(stderr, err)
	}
	for _, name := range []string{"cert9.db", "key4.db"} {
		if info, err := os.Stat(filepath.Join(directory, name)); err != nil || !info.Mode().IsRegular() {
			return reportError(stderr, fmt.Errorf("Firefox profile has no NSS %s", name))
		}
	}
	if err := ensureFirefoxProfileClosed(filepath.Join(directory, "cookies.sqlite")); err != nil {
		return reportError(stderr, err)
	}
	if operation == "list" {
		output, err := runNSSCommand("certutil", "-L", "-d", "sql:"+directory)
		if err != nil {
			return reportError(stderr, err)
		}
		return encodeBrowserNative(stdout, map[string]string{"text": string(output)})
	}
	if _, err := exec.LookPath("pk12util"); err != nil {
		return reportError(stderr, errors.New("pk12util is required for Firefox certificate sharing"))
	}
	temporary, err := os.MkdirTemp("", "ctx-certificate-")
	if err != nil {
		return reportError(stderr, err)
	}
	defer os.RemoveAll(temporary)
	archive := filepath.Join(temporary, "identity.p12")
	baseArgs := []string{"-d", "sql:" + directory, "-w", *passwordFile}
	if *slotPasswordFile != "" {
		baseArgs = append(baseArgs, "-k", *slotPasswordFile)
	}
	if operation == "export" {
		commandArgs := append([]string{"-o", archive, "-n", *nickname, "-c", "AES-256-CBC", "-C", "AES-256-CBC"}, baseArgs...)
		if _, err := runNSSCommand("pk12util", commandArgs...); err != nil {
			return reportError(stderr, err)
		}
		info, err := os.Stat(archive)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 8<<20 {
			return reportError(stderr, errors.New("pk12util did not produce a valid PKCS#12 archive under 8 MiB"))
		}
		content, err := os.ReadFile(archive)
		if err != nil {
			return reportError(stderr, err)
		}
		payload, err := json.Marshal(firefoxCertificatePayload{Format: "pkcs12", Nickname: *nickname, Data: base64.StdEncoding.EncodeToString(content)})
		if err != nil {
			return reportError(stderr, err)
		}
		return encodeBrowserNative(stdout, browserResourceBundle{Version: share.Version, Resource: "certificate", Payload: payload})
	}
	if request.Replace {
		return reportError(stderr, errors.New("certificate replacement is unavailable; remove the existing identity in Firefox first"))
	}
	if request.Bundle == nil {
		return reportErrorCode(stderr, errors.New("certificate import needs a bundle"), 2)
	}
	if err := validateBrowserResourceBundle(*request.Bundle, "certificate"); err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	var payload firefoxCertificatePayload
	if err := json.Unmarshal(request.Bundle.Payload, &payload); err != nil || payload.Format != "pkcs12" || payload.Data == "" {
		return reportErrorCode(stderr, errors.New("certificate bundle must contain password-protected PKCS#12 data"), 2)
	}
	content, err := base64.StdEncoding.DecodeString(payload.Data)
	if err != nil || len(content) == 0 || len(content) > 8<<20 {
		return reportErrorCode(stderr, errors.New("certificate bundle contains invalid PKCS#12 data"), 2)
	}
	if err := os.WriteFile(archive, content, 0o600); err != nil {
		return reportError(stderr, err)
	}
	if _, err := runNSSCommand("pk12util", append([]string{"-i", archive}, baseArgs...)...); err != nil {
		return reportError(stderr, err)
	}
	return 0
}

func validateCertificatePasswordFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 4096 {
		return errors.New("certificate password file must be a nonempty regular file under 4 KiB")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return errors.New("certificate password file must be private (mode 0600)")
	}
	return nil
}

func runNSSCommand(name string, args ...string) ([]byte, error) {
	if _, err := exec.LookPath(name); err != nil {
		return nil, fmt.Errorf("%s is required for Firefox certificate sharing", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.Output()
	if err == nil {
		return output, nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s timed out", name)
	}
	if failure, ok := err.(*exec.ExitError); ok {
		message := strings.TrimSpace(string(failure.Stderr))
		if message != "" {
			return nil, fmt.Errorf("%s: %s", name, message)
		}
	}
	return nil, fmt.Errorf("%s failed: %w", name, err)
}
