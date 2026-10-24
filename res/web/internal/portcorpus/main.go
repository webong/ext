// Command portcorpus writes the corpus the Swift and Java ports of this module are checked
// against: for a few thousand inputs it records what this implementation answers, so a port that
// agrees on all of them has the same behavior on what the fixtures in testdata do not cover
// (URL hosts and escapes, IP literals in cookie domains, id derivation, bookmarklet decoding).
//
// Run it with the toolchain the repository builds with and GODEBUG=urlstrictcolons=1, which the
// ports follow for hosts with several colons:
//
//	GODEBUG=urlstrictcolons=1 go run ./internal/portcorpus corpus.json
//
// scripts/ext-web-differential.sh does that and runs both ports against the result.
package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/webong/ext/res/web/bookmarklet"
	"github.com/webong/ext/res/web/contract"
	"github.com/webong/ext/res/web/userscript"
)

type siteCase struct {
	Input       string `json:"input"`
	OK          bool   `json:"ok"`
	String      string `json:"string,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
	Host        string `json:"host,omitempty"`
	Path        string `json:"path,omitempty"`
	EscapedPath string `json:"escapedPath,omitempty"`
	RawQuery    string `json:"rawQuery,omitempty"`
	ForceQuery  bool   `json:"forceQuery,omitempty"`
	Fragment    string `json:"fragment,omitempty"`
}

func sites() []siteCase {
	hosts := []string{"example.com", "EXAMPLE.com", "a.b.c.d", "localhost", "127.0.0.1", "1.2.3.4.5", "[::1]", "[::1]:8080", "[fe80::1%25en0]", "[fe80::1%en0]", "[fe80::1%25]", "[1.2.3.4]", "[::ffff:1.2.3.4]", "[::g]", "[::1", "::1", "example.com:80", "example.com:", "example.com:abc", "example.com:80:90", "a b.com", "ex%41mple.com", "b%C3%BCcher.de", "BÜCHER.de", "%FF.com", "%C3.com", "%zz.com", "a_b.com", "a$b.com", "a<b>.com", "a|b.com", "a^b.com", "a`b.com", "a{b}.com", "user@example.com", "user:pw@example.com", "@example.com", ":@example.com", "", "[", "]", "a]b.com", "[a]b", "日本語.jp", "%25.com", "ex%2541.com", "ex.com.", "exAmple.com:0080", "[::1]:", "[::1]:x", "[::1]x", "a[::1]", "[]", "[%25]", "[::1%25a%20b]", "[::1%25a%0Ab]", "[::1%25a%2fb]", "İSTANBUL.tr", "ΣΣ.gr", "xn--bcher-kva.de", "K.com", "%e2%82%ac.com", "a b.com", "a'b.com", "a\"b.com", "a!b.com", "a(b).com", "a*b.com", "a+b.com", "a,b.com", "a;b.com", "a=b.com", "a~b.com", "a-b.com", "%E2%82.com", "%E2%82%ACx.COM", "%C3%28.com"}
	paths := []string{"", "/", "/a", "/a/b/", "/a%20b", "/a b", "/a%2Fb", "/a%2fb", "/a%zz", "/%41", "/ü", "/a?b", "//x", "/..", "/*", "/a;b,c", "/a|b", "/{x}", "/%7Bx%7D", "/a%", "/a%2", "/a b", "/ ", "/%00", "/a#b", "/a@b:c", "/[x]", "/a%3Fb", "/a'b", "/a\"b", "/a<b>", "/a`b", "/a\\b", "/a^b", "/%E2%82", "/%E2%82%AC", "/%F0%9F%98", "/%C3%28", "/%ED%A0%80", "/%F4%90%80%80", "/%80"}
	suffixes := []string{"", "?", "?x=1", "?x=%zz", "?a?b", "#f", "#f%20g", "#%zz", "#", "?x#f", "#f#g", "#a b", "#aüb", "#!()*'", "#a%2Fb", "?x y", "# "}
	var out []siteCase
	add := func(s string) {
		if !utf8.ValidString(s) {
			return
		}
		c := siteCase{Input: s}
		u, err := contract.ParseSite(s)
		if err == nil {
			c.OK = true
			c.String = u.String()
			c.Hostname = u.Hostname()
			c.Host = u.Host
			c.Path = u.Path
			c.EscapedPath = u.EscapedPath()
			c.RawQuery = u.RawQuery
			c.ForceQuery = u.ForceQuery
			c.Fragment = u.Fragment
		}
		out = append(out, c)
	}
	for _, h := range hosts {
		for _, p := range paths {
			for _, s := range suffixes {
				add("https://" + h + p + s)
			}
		}
	}
	for _, scheme := range []string{"http", "HTTP", "Https", "ftp", "", "ws", "h+t", "1http", "http ", "ht tp", "http\t"} {
		for _, h := range []string{"example.com", "", "[::1]"} {
			for _, p := range []string{"", "/", "/a?b#c"} {
				add(scheme + "://" + h + p)
			}
		}
	}
	for _, s := range []string{"", "https:", "https:/", "https://", "https:///", "https:////a", "https:a", "//a", "https:\x00//a", "https://a\x7f", "https://a.com/\x01", "https://a.com#\x01", "https://a.com?\x01", ":", "::::", "http://a.com/\n", "http://\n", "*", "http://*", "http://a.com/*", "HTTPS://A.COM", "https://a.com "} {
		add(s)
	}
	// mutate seeds, deterministically
	r := rand.New(rand.NewSource(7))
	seeds := []string{"https://Example.com:8080/a%20b/c?x=1&y=%41#frag%20ment", "http://[fe80::1%25en0]:80/p", "https://b%C3%BCcher.de/%7Efoo?q#f"}
	alphabet := []byte("%:/?#@[]ab09. \x80\xc3\xbc\"<>{}|\\^`!$&'()*+,;=-_~")
	for i := 0; i < 20000; i++ {
		b := []byte(seeds[r.Intn(len(seeds))])
		for n := r.Intn(3) + 1; n > 0; n-- {
			switch r.Intn(3) {
			case 0:
				b[r.Intn(len(b))] = alphabet[r.Intn(len(alphabet))]
			case 1:
				at := r.Intn(len(b) + 1)
				b = append(b[:at], append([]byte{alphabet[r.Intn(len(alphabet))]}, b[at:]...)...)
			default:
				if len(b) > 1 {
					at := r.Intn(len(b))
					b = append(b[:at], b[at+1:]...)
				}
			}
		}
		add(string(b))
	}
	return out
}

type domainCase struct {
	Domain string `json:"domain"`
	Valid  bool   `json:"valid"`
}

func domains() []domainCase {
	tokens := []string{"::", ":", "1", "a", "ffff", "1.2.3.4", "01.2.3.4", "256.1.1.1", "%eth0", "%", "g", "12345", "0", ".", "x", "[", "]", "..", "-"}
	r := rand.New(rand.NewSource(11))
	var out []domainCase
	check := func(d string) {
		if !utf8.ValidString(d) {
			return
		}
		err := contract.ValidateCookie(contract.Cookie{Name: "a", Value: "v", Domain: d, Path: "/"})
		out = append(out, domainCase{d, err == nil})
	}
	for _, d := range []string{"::1", "::", "fe80::1", "::ffff:1.2.3.4", "1::", "1:2:3:4:5:6:7:8", "1:2:3:4:5:6:7:8:9", "1::2::3", "::1%eth0", "1.2.3.4", "example.com", ".example.com", "..example.com", "ex ample.com", "a:b", "[::1]", ":", "::ffff:1.2.3", "1:2:3:4:5:6:1.2.3.4", "::1.2.3.4", "1:2:3:4:5:6:7::", "1:2:3:4:5:6:7:8::", "::1:2:3:4:5:6:7", "::1:2:3:4:5:6:7:8"} {
		check(d)
		check("." + d)
	}
	for i := 0; i < 6000; i++ {
		var sb strings.Builder
		for n := r.Intn(8) + 1; n > 0; n-- {
			sb.WriteString(tokens[r.Intn(len(tokens))])
		}
		check(sb.String())
	}
	return out
}

type idCase struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	Out  string `json:"outName"`
}

func ids() []idCase {
	names := []string{"Hello World", "İstanbul", "KK", "Ａbc", "  spaced  name  ", "---", "...", "a.b-c_d", "ÀÉ", "ǅ", "straße", "ΑΣ", "ẋy", "..a..", "-.-a-.-", "a/b\\c", "TAB\tNAME", "emoji \U0001F600 x", "UPPER_lower-123.x", " nbsp ", " sep", "i̇", "ẞ", "Σ"}
	var out []idCase
	for _, n := range names {
		rec := userscript.InferMetadata(userscript.Record{Source: "// ==UserScript==\n// @name " + n + "\n// ==/UserScript==\n"}, "")
		out = append(out, idCase{n, rec.ID, rec.Name})
		rec = userscript.InferMetadata(userscript.Record{Name: n}, "")
		out = append(out, idCase{"direct:" + n, rec.ID, rec.Name})
	}
	for _, f := range []string{"/x/my-script.user.js", "my.script", "a/b/", "/", "", ".hidden", "dir.d/file", "a//b.c.d", "x.", "/.js"} {
		rec := userscript.InferMetadata(userscript.Record{Source: ""}, f)
		out = append(out, idCase{"file:" + f, rec.ID, rec.Name})
	}
	return out
}

type bmCase struct {
	Input  string `json:"input"`
	Encode bool   `json:"encodeOK"`
	URL    string `json:"url,omitempty"`
	Decode bool   `json:"decodeOK"`
	Source string `json:"source,omitempty"`
}

func bookmarklets() []bmCase {
	var out []bmCase
	add := func(s string) {
		if !utf8.ValidString(s) {
			return
		}
		c := bmCase{Input: s}
		if u, err := bookmarklet.Encode(s); err == nil {
			c.Encode, c.URL = true, u
		}
		if src, err := bookmarklet.Decode(s); err == nil {
			c.Decode, c.Source = true, src
		}
		out = append(out, c)
	}
	toks := []string{"javascript:", "JAVASCRIPT:", "JavaScrİpt:", " javascript:", "\tjavascript:", "javascript", "%41", "%4", "%zz", "%", "+", "void (function(){%0A", "%0A%7D).call(window)%3B", "void (function(){\n", "\n}).call(window);", "abc", "%E2%82%AC", "%FF", "%C3", "%00", "€", "ü", " ", "\n", "%0a", "%0A", "'", "\"", "%25", "%2541", "a b", "~", "-._"}
	r := rand.New(rand.NewSource(3))
	for i := 0; i < 8000; i++ {
		var sb strings.Builder
		for n := r.Intn(6) + 1; n > 0; n-- {
			sb.WriteString(toks[r.Intn(len(toks))])
		}
		add(sb.String())
		add("javascript:" + sb.String())
	}
	for _, s := range []string{"", " ", "\x00", "x\x00y", "alert(1)", " ", "  JAVASCRIPT:alert(1)", " "} {
		add(s)
	}
	return out
}

func main() {
	doc := map[string]any{"sites": sites(), "domains": domains(), "ids": ids(), "bookmarklets": bookmarklets()}
	f, err := os.Create(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	if err := enc.Encode(doc); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
