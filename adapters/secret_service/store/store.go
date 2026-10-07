// Package store owns Secret Service identities and command interaction.
package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/webong/ext/res/credential/adapterkit"
)

type SecretService struct{}

var ErrNotFound = errors.New("Secret Service item not found")
var validAttribute = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

func (SecretService) Check(context.Context) error {
	if _, err := exec.LookPath("secret-tool"); err != nil {
		return errors.New("secret-tool is required for Secret Service access")
	}
	return nil
}

func (SecretService) Get(ctx context.Context, item string) ([]byte, error) {
	attributes, err := parseItem(item)
	if err != nil {
		return nil, err
	}
	return Lookup(ctx, attributes)
}

func (SecretService) Put(ctx context.Context, item string, value []byte, replace bool) error {
	attributes, err := parseItem(item)
	if err != nil {
		return err
	}
	if !replace {
		if _, err := Lookup(ctx, attributes); err == nil {
			return adapterkit.ErrExists
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	if _, err := exec.LookPath("secret-tool"); err != nil {
		return errors.New("secret-tool is required for Secret Service access")
	}
	label := attributes["service"]
	if label == "" {
		label = "CTX credential"
	}
	args := append([]string{"store", "--label=" + label}, attributeArguments(attributes)...)
	command := exec.CommandContext(ctx, "secret-tool", args...)
	command.Stdin = bytes.NewReader(value)
	if err := command.Run(); err != nil {
		return fmt.Errorf("Secret Service store failed: %w", err)
	}
	return nil
}

// Lookup permits the owning browser adapter to supply its native Secret
// Service attributes without putting those conventions in CTX core.
func Lookup(ctx context.Context, attributes map[string]string) ([]byte, error) {
	if _, err := exec.LookPath("secret-tool"); err != nil {
		return nil, errors.New("secret-tool is required for Secret Service access")
	}
	args := append([]string{"lookup"}, attributeArguments(attributes)...)
	command := exec.CommandContext(ctx, "secret-tool", args...)
	output, err := command.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && len(exit.Stderr) == 0 {
			return nil, ErrNotFound
		}
		return nil, errors.New("Secret Service lookup failed")
	}
	// secret-tool adds a display newline only for a terminal. Output() uses a
	// pipe, so its bytes must be preserved exactly, including trailing newlines.
	if len(output) == 0 {
		return nil, ErrNotFound
	}
	return output, nil
}

func parseItem(item string) (map[string]string, error) {
	values, err := url.ParseQuery(item)
	if err != nil || len(values) == 0 || len(values) > 8 {
		return nil, errors.New("Secret Service item must be URL-encoded attributes, such as service=ctx&account=work")
	}
	attributes := make(map[string]string, len(values))
	for key, candidates := range values {
		if !validAttribute.MatchString(key) || len(candidates) != 1 || candidates[0] == "" || strings.ContainsRune(candidates[0], 0) {
			return nil, errors.New("invalid Secret Service item attribute")
		}
		attributes[key] = candidates[0]
	}
	return attributes, nil
}

func attributeArguments(attributes map[string]string) []string {
	keys := make([]string, 0, len(attributes))
	for key := range attributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// Retain the native service/account lookup order used by existing browser
	// adapters; the attribute pair itself remains order-independent.
	if _, service := attributes["service"]; service {
		if _, account := attributes["account"]; account {
			for i, key := range keys {
				if key == "service" {
					keys = append([]string{"service"}, append(keys[:i], keys[i+1:]...)...)
					break
				}
			}
		}
	}
	args := make([]string, 0, 2*len(keys))
	for _, key := range keys {
		args = append(args, key, attributes[key])
	}
	return args
}
