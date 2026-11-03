package userscript

import (
	"encoding/json"
	"errors"
)

// ReplaySource builds a page-world, URL-scoped registration. The document-local
// marker prevents a script from running twice when attachment races navigation.
func ReplaySource(id, revision, source string, matches, excludes []string) (string, error) {
	record := Record{Target: "session", ID: id, Name: id, Source: source, Matches: matches, ExcludeMatches: excludes}
	record.Revision = Revision(record)
	if err := Validate(record); err != nil {
		return "", err
	}
	if revision == "" {
		return "", errors.New("userscript registration needs a reviewed revision")
	}
	values, err := json.Marshal([]any{id, revision, source, matches, excludes})
	if err != nil {
		return "", err
	}
	return `(() => {
 const [id, revision, source, matches, excludes] = ` + string(values) + `;
 const glob = value => new RegExp('^' + value.replace(/[.+?^${}()|[\]\\]/g, '\\$&').replaceAll('*', '.*') + '$');
 const allowed = pattern => {
  const parts = pattern.match(/^(\*|https?|file):\/\/([^/]+)\/(.*)$/);
  if (!parts) return false;
  const url = new URL(location.href);
  if (parts[1] === '*' ? !['http:', 'https:'].includes(url.protocol) : url.protocol !== parts[1]+':') return false;
  const host = parts[2];
  if (host !== '*' && !(host.startsWith('*.') ? url.hostname === host.slice(2) || url.hostname.endsWith(host.slice(1)) : url.hostname === host)) return false;
  return glob('/'+parts[3]).test(url.pathname+url.search);
 };
 if (!matches.some(allowed) || (excludes || []).some(allowed)) return;
 const applied = globalThis[Symbol.for('ctx.userscripts.applied')] ||= new Map();
 if (applied.get(id) === revision) return;
 applied.set(id, revision);
 (0, eval)(source);
})()`, nil
}
