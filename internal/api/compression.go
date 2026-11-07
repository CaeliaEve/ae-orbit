package api

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/CaeliaEve/ae-orbit/internal/telemetry"
)

const inventoryCompressionThreshold = 1024

var inventoryCompressors = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	return w
}}

// Only bounded inventory responses use compression. Credential responses, OC
// messages and streaming events continue through their existing writers.
func respondInventory(w http.ResponseWriter, r *http.Request, value any) {
	w.Header().Add("Vary", "Accept-Encoding")
	gzipAllowed := acceptsEncoding(r.Header, "gzip", false)
	identityAllowed := acceptsEncoding(r.Header, "identity", true)
	if !gzipAllowed && !identityAllowed {
		fail(w, httpError{http.StatusNotAcceptable, "encoding_not_acceptable"})
		return
	}
	body, err := json.Marshal(value)
	if err != nil {
		fail(w, err)
		return
	}
	body = append(body, '\n')
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodHead {
		telemetry.UncompressedBytes(r.Context(), len(body))
	}
	if gzipAllowed && (len(body) >= inventoryCompressionThreshold || !identityAllowed) {
		var encoded bytes.Buffer
		compressor := inventoryCompressors.Get().(*gzip.Writer)
		compressor.Reset(&encoded)
		_, writeErr := compressor.Write(body)
		closeErr := compressor.Close()
		compressor.Reset(io.Discard)
		inventoryCompressors.Put(compressor)
		if writeErr == nil && closeErr == nil && (encoded.Len() < len(body) || !identityAllowed) {
			w.Header().Set("Content-Encoding", "gzip")
			body = encoded.Bytes()
		}
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func acceptsEncoding(header http.Header, wanted string, defaultAllowed bool) bool {
	seen, allowed, wildcardSeen, wildcardAllowed := false, false, false, false
	for _, field := range header.Values("Accept-Encoding") {
		for _, entry := range strings.Split(field, ",") {
			parts := strings.Split(entry, ";")
			coding := strings.TrimSpace(parts[0])
			if !strings.EqualFold(coding, wanted) && coding != "*" {
				continue
			}
			positive := true
			if len(parts) > 1 {
				name, value, found := strings.Cut(strings.TrimSpace(parts[1]), "=")
				positive = len(parts) == 2 && found && strings.EqualFold(strings.TrimSpace(name), "q") && positiveQuality(strings.TrimSpace(value))
			}
			if strings.EqualFold(coding, wanted) {
				allowed = positive && (!seen || allowed)
				seen = true
			} else {
				wildcardAllowed = positive && (!wildcardSeen || wildcardAllowed)
				wildcardSeen = true
			}
		}
	}
	if seen {
		return allowed
	}
	if wildcardSeen {
		return wildcardAllowed
	}
	return defaultAllowed
}

// RFC 9110 qvalues allow 0 or 1, with at most three fractional digits.
func positiveQuality(value string) bool {
	whole, fraction, decimal := strings.Cut(value, ".")
	if whole != "0" && whole != "1" || decimal && len(fraction) > 3 {
		return false
	}
	positive := whole == "1"
	for _, digit := range fraction {
		if digit < '0' || digit > '9' || whole == "1" && digit != '0' {
			return false
		}
		positive = positive || digit != '0'
	}
	return positive
}
