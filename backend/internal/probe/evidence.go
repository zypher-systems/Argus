package probe

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"argus/internal/store"
)

const maxEvidenceBody = 2048

func finalizeEvidence(kind string, out *store.CheckEvidence, err error) {
	if out == nil {
		return
	}
	out.Kind = kind
	if out.At.IsZero() {
		out.At = time.Now().UTC()
	}
	out.Success = err == nil
	if err != nil {
		out.Error = err.Error()
		if out.TLSError == "" {
			out.TLSError = classifyTLSError(err)
		}
	}
}

func classifyTLSError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "certificate has expired") || strings.Contains(msg, "expired"):
		if strings.Contains(msg, "cert") || strings.Contains(msg, "tls") || strings.Contains(msg, "x509") {
			return "expired"
		}
	}
	switch {
	case strings.Contains(msg, "x509") && (strings.Contains(msg, "valid for") || strings.Contains(msg, "not valid for") || strings.Contains(msg, "hostname")):
		return "hostname mismatch"
	case strings.Contains(msg, "unknown authority") || strings.Contains(msg, "signed by unknown"):
		return "unknown authority"
	case strings.Contains(msg, "tls:") || strings.Contains(msg, "x509:") || strings.Contains(msg, "handshake"):
		return "handshake"
	default:
		return ""
	}
}

func snippetBody(b []byte, n int) string {
	if n <= 0 {
		n = maxEvidenceBody
	}
	if len(b) > n {
		b = b[:n]
	}
	if !utf8.Valid(b) {
		b = []byte(strings.ToValidUTF8(string(b), "\uFFFD"))
	}
	var bld strings.Builder
	bld.Grow(len(b))
	for _, r := range string(b) {
		if r == '\n' || r == '\r' || r == '\t' || r >= 32 {
			bld.WriteRune(r)
		} else {
			bld.WriteRune('\uFFFD')
		}
	}
	return bld.String()
}

func httpCurl(req *http.Request) string {
	if req == nil || req.URL == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("curl -sS -X ")
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	b.WriteString(method)
	b.WriteString(" ")
	b.WriteString("'")
	b.WriteString(req.URL.String())
	b.WriteString("'")
	for k, vals := range req.Header {
		if strings.EqualFold(k, "Authorization") {
			b.WriteString(" -H 'Authorization: <redacted>'")
			continue
		}
		for _, v := range vals {
			b.WriteString(" -H '")
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("'")
		}
	}
	return b.String()
}
