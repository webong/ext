package bundle

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

//go:embed shim.js
var shim string

// Shim returns the page script with its token placeholder left in place, which
// is what an embedded host injects at document start. The script then reports
// through the app's native message handler instead of over HTTP.
func Shim() string { return shim }

const controlPrefix = "/__ext/"

func (s *session) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	header := w.Header()
	header.Set("Content-Security-Policy", s.policy)
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	if s.isolated {
		header.Set("Cross-Origin-Opener-Policy", "same-origin")
		header.Set("Cross-Origin-Embedder-Policy", "require-corp")
		header.Set("Cross-Origin-Resource-Policy", "same-origin")
	}
	// A page on another site must not be able to talk to this server through a
	// rebound DNS name, so only the exact loopback address is answered.
	if r.Host != s.host {
		http.Error(w, "unexpected host", http.StatusMisdirectedRequest)
		return
	}
	if strings.HasPrefix(r.URL.Path, controlPrefix) {
		s.control(w, r)
		return
	}
	s.static(w, r)
}

// control serves the shim and takes the page's reports. The path carries a
// random token, and a report must come from this origin.
func (s *session) control(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, controlPrefix)
	token, name, ok := strings.Cut(rest, "/")
	if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1 {
		http.NotFound(w, r)
		return
	}
	if name == "shim.js" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		io.WriteString(w, strings.ReplaceAll(shim, "%TOKEN%", s.token))
		return
	}
	if name == "next" && r.Method == http.MethodGet && s.sameOrigin(r) {
		s.next(w, r)
		return
	}
	if r.Method != http.MethodPost || !s.sameOrigin(r) {
		http.Error(w, "not allowed", http.StatusForbidden)
		return
	}
	var body struct {
		Level string `json:"level"`
		Text  string `json:"text"`
		Code  int    `json:"code"`
		Data  string `json:"data"`
	}
	limit := int64(maxBodyBytes)
	if name == "host" {
		limit = 2*maxMessageBytes + 1<<10 // JSON escaping can double a message
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || int64(len(data)) > limit || (len(data) > 0 && json.Unmarshal(data, &body) != nil) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	switch name {
	case "alive":
		s.touch()
	case "log":
		s.touch()
		s.emit(cleanLevel(body.Level), body.Text)
	case "host":
		s.touch()
		if len(body.Data) > maxMessageBytes {
			http.Error(w, "message too large", http.StatusRequestEntityTooLarge)
			return
		}
		if s.onMessage != nil {
			s.onMessage(body.Data)
		}
	case "exit":
		s.touch()
		s.finish(Result{Status: StatusExited, ExitCode: body.Code})
	case "closed":
		s.finish(Result{Status: StatusClosed, Reason: "the page was closed"})
	default:
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// next answers the page's request for the host's next message: the message, or
// "nothing yet" so the page asks again. A request that stays open counts as the
// page being alive.
func (s *session) next(w http.ResponseWriter, r *http.Request) {
	s.touch()
	timer := time.NewTimer(pollWait)
	defer timer.Stop()
	select {
	case message := <-s.outbox:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, message)
	case <-timer.C:
		w.WriteHeader(http.StatusNoContent)
	case <-r.Context().Done():
	}
}

// sameOrigin accepts a report that names this server as its origin, and one
// that names no origin at all (some browsers omit it for same-origin requests).
func (s *session) sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	origin := r.Header.Get("Origin")
	return origin == "" || origin == "http://"+s.host
}

func cleanLevel(level string) string {
	switch level {
	case "log", "info", "warn", "error", "debug", "exception":
		return level
	}
	return "log"
}

func (s *session) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	requested, err := url.PathUnescape(r.URL.Path)
	if err != nil || strings.ContainsRune(requested, 0) {
		http.NotFound(w, r)
		return
	}
	// "/" shows the entry. A JavaScript entry gets a generated page around it.
	if requested == "/" {
		if ext := strings.ToLower(path.Ext(s.entry)); ext == ".js" || ext == ".mjs" {
			s.page(w, r, `<script type="module" src="/`+escapePath(s.entry)+`"></script>`)
			return
		}
		requested = "/" + s.entry
	}
	file, info, err := s.root.open(requested)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	if info.Size() > maxFileBytes {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}
	contentType := mime.TypeByExtension(strings.ToLower(path.Ext(requested)))
	if strings.HasPrefix(contentType, "text/html") {
		data, err := io.ReadAll(io.LimitReader(file, maxFileBytes))
		if err != nil {
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method != http.MethodHead {
			w.Write(withShim(data, s.token))
		}
		return
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, "", info.ModTime(), file)
}

func (s *session) page(w http.ResponseWriter, r *http.Request, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		return
	}
	io.WriteString(w, `<!doctype html><html><head><meta charset="utf-8"><title>ext</title>`+shimTag(s.token)+`</head><body>`+body+`</body></html>`)
}

func shimTag(token string) string {
	return `<script src="` + controlPrefix + token + `/shim.js"></script>`
}

// withShim puts the shim before any other script, so console output and errors
// from the page are reported from the start.
func withShim(html []byte, token string) []byte {
	tag := []byte(shimTag(token))
	lower := strings.ToLower(string(html))
	for _, marker := range []string{"<head>", "<head "} {
		if i := strings.Index(lower, marker); i >= 0 {
			if end := strings.IndexByte(lower[i:], '>'); end >= 0 {
				at := i + end + 1
				return append(append(append([]byte{}, html[:at]...), tag...), html[at:]...)
			}
		}
	}
	return append(append([]byte{}, tag...), html...)
}

func escapePath(p string) string {
	parts := strings.Split(path.Clean(p), "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}
