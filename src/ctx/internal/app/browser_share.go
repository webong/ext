package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/webong/ext/res/browser"
	browsershare "github.com/webong/ext/res/browser/contract"
	"github.com/webong/ext/src/ctx/internal/config"
	modpkg "github.com/webong/ext/src/ctx/internal/mod"
)

type browserEndpoint struct {
	Adapter *modpkg.Adapter
	Profile string
}

type browserCookie = browsershare.Cookie

// Browser adapters exchange one versioned request per share operation. The
// cookie value is returned only by export, never by list.
type browserShareRequest = browsershare.CookieRequest

func browserAdapterShare(resolver *config.Resolver, endpoint browserEndpoint, resource, operation string, request browserShareRequest, response any, stderr io.Writer) error {
	request.Version = browsershare.Version
	return invokeBrowserShareJSON(resolver, endpoint, resource, operation, request, response, stderr)
}

func invokeBrowserShareJSON(resolver *config.Resolver, endpoint browserEndpoint, resource, operation string, request any, response any, stderr io.Writer) error {
	if !endpoint.Adapter.HasBrowserShare(resource + "." + operation) {
		return fmt.Errorf("%s adapter does not support browser %s %s", endpoint.Adapter.Manifest.Name, resource, operation)
	}
	inventory, err := freshMachineInventory(resolver)
	if err != nil {
		return fmt.Errorf("read machine graph: %w", err)
	}
	if _, err := inventory.Find(endpoint.Adapter.Manifest.Name, endpoint.Profile); err == nil {
		if _, err := inventory.Find(endpoint.Adapter.Manifest.Name, endpoint.Profile, "share"); err != nil {
			return err
		}
	}
	input, err := json.Marshal(request)
	if err != nil {
		return err
	}
	var output bytes.Buffer
	code := invokeAdapterIO(resolver, endpoint.Adapter, "share", endpoint.Profile, []string{resource, operation}, "", bytes.NewReader(input), &output, stderr)
	if code != 0 {
		return fmt.Errorf("%s adapter %s %s failed (exit %d)", endpoint.Adapter.Manifest.Name, resource, operation, code)
	}
	if response != nil {
		if err := json.Unmarshal(output.Bytes(), response); err != nil {
			return fmt.Errorf("%s adapter returned invalid %s %s JSON: %w", endpoint.Adapter.Manifest.Name, resource, operation, err)
		}
	}
	return nil
}

type browserCookieBundle = browsershare.CookieBundle

type cookieAttributeFilter map[string]string

func (filter *cookieAttributeFilter) String() string { return fmt.Sprint(map[string]string(*filter)) }
func (filter *cookieAttributeFilter) Set(raw string) error {
	key, value, ok := strings.Cut(raw, "=")
	if !ok || !strings.Contains(key, ".") || key == "" || len(key) > 128 || len(value) > 1024 || strings.ContainsAny(key, " \t\r\n") {
		return errors.New("attribute must be namespaced: key=value")
	}
	if *filter == nil {
		*filter = make(cookieAttributeFilter)
	}
	(*filter)[key] = value
	return nil
}

func cookieMatchesAttributes(cookie browserCookie, filter cookieAttributeFilter) bool {
	for key, value := range filter {
		actual, exists := cookie.Attributes[key]
		if !exists || actual != value {
			return false
		}
	}
	return true
}

func shareBrowserCommand(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 && args[0] == "cookie" && args[1] == "normalize" {
		return shareBrowserCookieNormalize(args[2:], stdout, stderr)
	}
	if len(args) > 1 && args[0] == "cookie" && args[1] == "query" {
		return shareBrowserCookieQuery(resolver, args[2:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "capabilities" {
		flags := flag.NewFlagSet("share:browser capabilities", flag.ContinueOnError)
		flags.SetOutput(stderr)
		from := flags.String("from", "", "browser:profile")
		if err := flags.Parse(args[1:]); err != nil || len(flags.Args()) != 0 {
			return 2
		}
		endpoint, err := resolveBrowserSource(resolver, *from)
		if err != nil {
			return reportErrorCode(stderr, err, 2)
		}
		statuses := probeBrowserShare(resolver, endpoint.Adapter, endpoint.Profile)
		for _, capability := range endpoint.Adapter.Manifest.BrowserShare {
			fmt.Fprintf(stdout, "%s\t%s\n", capability, statuses[capability])
		}
		return 0
	}
	if len(args) > 1 && args[0] == "policy" && args[1] == "export" {
		return shareBrowserPolicyCommand(resolver, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] != "cookie" {
		return shareBrowserGenericCommand(resolver, args, stdout, stderr)
	}
	if len(args) > 1 && args[0] == "cookie" && args[1] == "import" {
		return shareBrowserCookieImport(resolver, args[2:], stdout, stderr)
	}
	if len(args) < 2 || args[0] != "cookie" || (args[1] != "list" && args[1] != "copy") {
		fmt.Fprintln(stderr, "ctx: share:browser requires capabilities, cookie list|query|normalize|copy|import, or policy export")
		return 2
	}
	action := args[1]
	flags := flag.NewFlagSet("share:browser cookie "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	from := flags.String("from", "", "source browser:profile (defaults to selected browser)")
	site := flags.String("site", "", "site URL")
	name := flags.String("name", "", "cookie name")
	domain := flags.String("domain", "", "exact cookie domain")
	path := flags.String("path", "", "exact cookie path")
	id := flags.Int64("id", 0, "cookie row ID from cookie list")
	ref := flags.String("ref", "", "opaque cookie reference from cookie list")
	var attributes cookieAttributeFilter
	flags.Var(&attributes, "attribute", "exact namespaced cookie attribute (key=value; repeatable)")
	toProfile := flags.String("to-profile", "", "destination browser:profile")
	toFile := flags.String("to-file", "", "new file for a JSON cookie bundle")
	toStdout := flags.Bool("stdout", false, "write a JSON cookie bundle to stdout")
	replace := flags.Bool("replace", false, "replace an existing target-profile cookie")
	if err := flags.Parse(args[2:]); err != nil {
		return 2
	}
	selectedFlags := map[string]bool{}
	flags.Visit(func(value *flag.Flag) { selectedFlags[value.Name] = true })
	if len(flags.Args()) != 0 || *site == "" {
		fmt.Fprintln(stderr, "ctx: specify --site <http-or-https-URL> and no positional arguments")
		return 2
	}
	siteURL, err := parseCookieSite(*site)
	if err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	source, err := resolveBrowserSource(resolver, *from)
	if err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	destinationCount := 0
	for _, present := range []bool{*toProfile != "", *toFile != "", *toStdout} {
		if present {
			destinationCount++
		}
	}
	if action == "list" {
		if destinationCount != 0 || selectedFlags["replace"] || selectedFlags["name"] || selectedFlags["domain"] || selectedFlags["path"] || selectedFlags["id"] || selectedFlags["ref"] || selectedFlags["attribute"] {
			fmt.Fprintln(stderr, "ctx: cookie list accepts --from and --site only")
			return 2
		}
	} else if *name == "" || (selectedFlags["id"] && *id <= 0) || destinationCount != 1 || (selectedFlags["replace"] && *toProfile == "") {
		fmt.Fprintln(stderr, "ctx: cookie copy needs --name and exactly one of --to-profile, --to-file, or --stdout; --replace only applies to --to-profile")
		return 2
	}
	var cookies []browserCookie
	if err := browserAdapterShare(resolver, source, "cookie", "list", browserShareRequest{Site: siteURL.String()}, &cookies, stderr); err != nil {
		return reportError(stderr, err)
	}
	filtered := make([]browserCookie, 0, len(cookies))
	for _, cookie := range cookies {
		cookie.Value = ""
		if err := browsershare.ValidateCookie(cookie); err != nil {
			return reportError(stderr, fmt.Errorf("source adapter returned invalid cookie metadata: %w", err))
		}
		if browsershare.CookieMatchesSite(siteURL, cookie) {
			filtered = append(filtered, cookie)
		}
	}
	cookies = filtered
	if action == "list" {
		for _, cookie := range cookies {
			encodedAttributes, _ := json.Marshal(cookie.Attributes)
			fmt.Fprintf(stdout, "%s\t%s\t%s\tsecure=%t\thttp_only=%t\texpiry=%d\tsame_site=%s\tid=%d\tref=%s\tattributes=%s\tpartition_key=%s\tancestor=%t\n",
				cookie.Name, cookie.Domain, cookie.Path, cookie.Secure, cookie.HTTPOnly, cookie.Expiry, cookie.SameSitePolicy, cookie.ID, cookie.Ref, encodedAttributes, cookie.PartitionKey, cookie.CrossSiteAncestor)
		}
		return 0
	}
	selected := make([]browserCookie, 0, 1)
	for _, cookie := range cookies {
		if cookie.Name == *name && (*domain == "" || cookie.Domain == *domain) && (*path == "" || cookie.Path == *path) && (*id == 0 || cookie.ID == *id) && (*ref == "" || cookie.Ref == *ref) && cookieMatchesAttributes(cookie, attributes) {
			selected = append(selected, cookie)
		}
	}
	if len(selected) == 0 {
		fmt.Fprintln(stderr, "ctx: no matching cookie; use cookie list to inspect site cookie names and scopes")
		return 1
	}
	if len(selected) != 1 {
		fmt.Fprintln(stderr, "ctx: multiple cookies match; add --id from cookie list (or narrow by --domain, --path, or --attribute)")
		return 1
	}
	cookie := selected[0]
	if *toProfile != "" {
		target, err := parseBrowserEndpoint(*toProfile)
		if err != nil {
			return reportErrorCode(stderr, err, 2)
		}
		if !target.Adapter.HasBrowserShare("cookie.import") {
			fmt.Fprintf(stderr, "ctx: %s adapter cannot import cookies into a profile\n", target.Adapter.Manifest.Name)
			return 1
		}
		if target.Adapter.Manifest.Name == source.Adapter.Manifest.Name && target.Profile == source.Profile {
			fmt.Fprintln(stderr, "ctx: source and target are the same browser profile")
			return 1
		}
		var exported browserCookie
		if err := browserAdapterShare(resolver, source, "cookie", "export", browserShareRequest{Site: siteURL.String(), Cookie: cookie}, &exported, stderr); err != nil {
			return reportError(stderr, err)
		}
		if !sameListedCookie(exported, cookie) {
			return reportError(stderr, errors.New("source adapter returned a different cookie than selected"))
		}
		bundle := browserCookieBundle{Version: browsershare.Version, Source: source.Adapter.Manifest.Name + ":" + source.Profile, Site: cookieBundleSite(siteURL), Cookie: exported}
		if err := validateBrowserCookieBundle(bundle); err != nil {
			return reportError(stderr, err)
		}
		if err := browserAdapterShare(resolver, target, "cookie", "import", browserShareRequest{Bundle: &bundle, Replace: *replace}, nil, stderr); err != nil {
			return reportError(stderr, err)
		}
		fmt.Fprintf(stdout, "shared cookie %s for %s into %s\n", cookie.Name, siteURL.Hostname(), *toProfile)
		return 0
	}
	if *toStdout {
		if file, ok := stdout.(*os.File); ok {
			info, err := file.Stat()
			if err != nil {
				return reportError(stderr, err)
			}
			if info.Mode()&os.ModeNamedPipe == 0 {
				fmt.Fprintln(stderr, "ctx: --stdout requires a pipe; use --to-file for a protected file")
				return 2
			}
		}
	} else if _, err := os.Lstat(*toFile); err == nil {
		fmt.Fprintln(stderr, "ctx: output file already exists; choose a new --to-file path")
		return 1
	} else if !errors.Is(err, os.ErrNotExist) {
		return reportError(stderr, err)
	}
	var exported browserCookie
	if err := browserAdapterShare(resolver, source, "cookie", "export", browserShareRequest{Site: siteURL.String(), Cookie: cookie}, &exported, stderr); err != nil {
		return reportError(stderr, err)
	}
	if !sameListedCookie(exported, cookie) {
		return reportError(stderr, errors.New("source adapter returned a different cookie than selected"))
	}
	bundle := browserCookieBundle{Version: browsershare.Version, Source: source.Adapter.Manifest.Name + ":" + source.Profile, Site: cookieBundleSite(siteURL), Cookie: exported}
	if err := validateBrowserCookieBundle(bundle); err != nil {
		return reportError(stderr, err)
	}
	if *toStdout {
		if err := json.NewEncoder(stdout).Encode(bundle); err != nil {
			return reportError(stderr, err)
		}
		return 0
	}
	if err := writeCookieBundle(*toFile, bundle); err != nil {
		return reportError(stderr, err)
	}
	fmt.Fprintf(stdout, "shared cookie %s for %s into %s (mode 0600)\n", cookie.Name, siteURL.Hostname(), *toFile)
	return 0
}

func shareBrowserCookieImport(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("share:browser cookie import", flag.ContinueOnError)
	flags.SetOutput(stderr)
	fromFile := flags.String("from-file", "", "cookie bundle, query JSON, or Netscape cookie file")
	fromStdin := flags.Bool("stdin", false, "read cookies from a pipe")
	toProfile := flags.String("to-profile", "", "destination browser:profile")
	site := flags.String("site", "", "site URL (required for input without a bundle site)")
	name := flags.String("name", "", "select cookie name from an export")
	domain := flags.String("domain", "", "select exact cookie domain")
	path := flags.String("path", "", "select exact cookie path")
	var attributes cookieAttributeFilter
	flags.Var(&attributes, "attribute", "exact namespaced cookie attribute (key=value; repeatable)")
	replace := flags.Bool("replace", false, "replace an existing cookie")
	if err := flags.Parse(args); err != nil || len(flags.Args()) != 0 || (*fromFile == "") == !*fromStdin || *toProfile == "" {
		fmt.Fprintln(stderr, "ctx: cookie import needs exactly one of --from-file or --stdin and --to-profile")
		return 2
	}
	target, err := parseBrowserEndpoint(*toProfile)
	if err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	if !target.Adapter.HasBrowserShare("cookie.import") {
		return reportError(stderr, fmt.Errorf("%s adapter cannot import cookies", target.Adapter.Manifest.Name))
	}
	var input io.Reader
	if *fromStdin {
		info, err := os.Stdin.Stat()
		if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
			return reportErrorCode(stderr, errors.New("--stdin requires a pipe"), 2)
		}
		input = os.Stdin
	} else {
		file, err := os.Open(*fromFile)
		if err != nil {
			return reportError(stderr, err)
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > browser.MaxCookieInputBytes {
			return reportError(stderr, errors.New("cookie input must be a regular file under 8 MiB"))
		}
		input = file
	}
	data, err := io.ReadAll(io.LimitReader(input, browser.MaxCookieInputBytes+1))
	if err != nil || len(data) > browser.MaxCookieInputBytes {
		return reportErrorCode(stderr, errors.New("cookie input exceeds 8 MiB or cannot be read"), 2)
	}
	cookies, err := browser.ParseCookies(data)
	if err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	var metadata struct {
		Site     string   `json:"site"`
		Warnings []string `json:"warnings"`
	}
	// Only ctx bundles have an implicit site. Other exports require --site.
	_ = json.Unmarshal(data, &metadata)
	if *site == "" {
		*site = metadata.Site
	}
	if *site == "" {
		return reportErrorCode(stderr, errors.New("cookie import needs --site for a query result or external export"), 2)
	}
	siteURL, err := parseCookieSite(*site)
	if err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	var selected []browser.Cookie
	for _, cookie := range cookies {
		if browsershare.CookieMatchesSite(siteURL, cookie.Cookie) && (*name == "" || cookie.Name == *name) &&
			(*domain == "" || cookie.Domain == *domain) && (*path == "" || cookie.Path == *path) && cookieMatchesAttributes(cookie.Cookie, attributes) {
			selected = append(selected, cookie)
		}
	}
	if len(selected) != 1 {
		return reportErrorCode(stderr, errors.New("cookie import needs exactly one matching active cookie; select --name, --domain, --path, or --attribute"), 2)
	}
	bundle, err := browser.BundleCookie(selected[0], cookieBundleSite(siteURL))
	if err != nil {
		return reportErrorCode(stderr, err, 2)
	}
	if len(metadata.Warnings) > 0 {
		fmt.Fprintf(stderr, "ctx: warning: input query reported %d warnings; importing only the selected cookie\n", len(metadata.Warnings))
	}
	if err := browserAdapterShare(resolver, target, "cookie", "import", browserShareRequest{Bundle: &bundle, Replace: *replace}, nil, stderr); err != nil {
		return reportError(stderr, err)
	}
	fmt.Fprintf(stdout, "imported cookie %s into %s\n", bundle.Cookie.Name, *toProfile)
	return 0
}

func parseCookieSite(raw string) (*url.URL, error) {
	return browsershare.ParseSite(raw)
}

func cookieBundleSite(site *url.URL) string {
	return (&url.URL{Scheme: site.Scheme, Host: site.Host, Path: site.Path, RawPath: site.RawPath}).String()
}

func resolveBrowserSource(resolver *config.Resolver, choice string) (browserEndpoint, error) {
	if choice == "" {
		choice = os.Getenv("CTX_BROWSER")
		if choice == "" {
			resolved, err := resolver.Resolve("browser")
			if err != nil {
				return browserEndpoint{}, err
			}
			choice = resolved.Value
		}
	}
	if choice == "" {
		return browserEndpoint{}, errors.New("no source browser selected; use --from <browser>:<profile> or ctx set <browser>:<profile>")
	}
	return parseBrowserEndpoint(choice)
}

func parseBrowserEndpoint(choice string) (browserEndpoint, error) {
	provider, profile, ok := strings.Cut(choice, ":")
	if !ok || provider == "" || profile == "" || strings.ContainsAny(profile, "\r\n") {
		return browserEndpoint{}, errors.New("browser endpoint must be <browser>:<profile>")
	}
	store := adapterStore()
	installed, err := store.Load(provider)
	if err != nil || !installed.IsRuntime("browser") {
		return browserEndpoint{}, fmt.Errorf("browser provider %s is not installed", provider)
	}
	if err := store.AssertTrusted(installed); err != nil {
		return browserEndpoint{}, err
	}
	return browserEndpoint{Adapter: installed, Profile: profile}, nil
}

func writeCookieBundle(path string, bundle browserCookieBundle) error {
	return writePrivateJSON(path, bundle)
}

func sameListedCookie(a, b browserCookie) bool {
	return browsershare.SameListedCookie(a, b)
}

func validateBrowserCookieBundle(bundle browserCookieBundle) error {
	return browsershare.ValidateCookieBundle(bundle)
}
