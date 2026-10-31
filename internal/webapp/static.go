package webapp

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"
)

// fontCacheControl caches the embedded woff2 files for 30 days. Their names are
// not content-hashed, so this trades a slow rollout of a font swap (rare) for
// not re-sending 64 KB on every mini app open (constant).
const fontCacheControl = "public, max-age=2592000"

// gzipMinSize is the body size below which compression is skipped: a short JSON
// error grows when gzipped, and the CPU is wasted either way. 1400 bytes is
// roughly one Ethernet MTU — below it the response already fits in one packet.
const gzipMinSize = 1400

// staticETags maps an embedded file path ("index.html") to a quoted ETag over
// its bytes. embed.FS reports a zero ModTime, so without this http.ServeContent
// emits no validator at all and every mini app open re-downloads the whole
// frontend instead of getting a 304.
var staticETags = sync.OnceValue(func() map[string]string {
	out := map[string]string{}
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return out
	}
	_ = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // a partial ETag map only costs us 304s
		}
		b, err := fs.ReadFile(sub, p)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(b)
		out[p] = `"` + hex.EncodeToString(sum[:8]) + `"`
		return nil
	})
	return out
})

// staticHandler serves the embedded frontend with a content ETag (so revisits
// are 304s) and cache headers: revalidate the app shell on every open, keep the
// fonts for a month.
func staticHandler() http.Handler {
	sub, _ := fs.Sub(staticFS, "static")
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "." {
			name = "index.html"
		}
		if etag, ok := staticETags()[name]; ok {
			w.Header().Set("ETag", etag)
		}
		if strings.HasPrefix(r.URL.Path, "/fonts/") {
			w.Header().Set("Cache-Control", fontCacheControl)
		} else {
			// no-cache means "revalidate", not "don't store": the ETag above still
			// turns a repeat open into a 304.
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

var gzipWriterPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(nil, gzip.DefaultCompression)
		return w
	},
}

// compressible reports whether a Content-Type is worth gzipping. woff2, images
// and the SQLite backup download are already compressed or opaque; re-encoding
// them only burns CPU.
func compressible(contentType string) bool {
	ct := contentType
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(strings.ToLower(ct))
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	switch ct {
	case "application/json", "application/javascript", "application/xml", "image/svg+xml":
		return true
	}
	return false
}

// gzipMiddleware compresses eligible responses for clients that accept gzip.
// The frontend alone is ~270 KB of HTML+CSS+JS in one file, and the admin JSON
// lists grow with the panel, so this is the difference between a fast mini app
// on mobile data and a slow one.
func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}
		// Caches must not serve a gzipped body to a client that cannot read it.
		w.Header().Add("Vary", "Accept-Encoding")
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.finish()
		next.ServeHTTP(gw, r)
	})
}

func acceptsGzip(r *http.Request) bool {
	for _, enc := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		if name, _, _ := strings.Cut(strings.TrimSpace(enc), ";"); strings.EqualFold(name, "gzip") {
			return true
		}
	}
	return false
}

// gzipResponseWriter buffers the first gzipMinSize bytes before choosing an
// encoding: the decision needs the status, the Content-Type and the body size,
// and none of them are known when the handler is entered.
type gzipResponseWriter struct {
	http.ResponseWriter
	status  int
	buf     []byte
	gz      *gzip.Writer
	decided bool
	gzipOn  bool
}

func (w *gzipResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if !w.decided {
		w.buf = append(w.buf, b...)
		if len(w.buf) < gzipMinSize {
			return len(b), nil
		}
		if err := w.decide(); err != nil {
			return 0, err
		}
		return len(b), nil
	}
	if w.gzipOn {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// decide commits to an encoding, sends the header, and flushes what was
// buffered. Everything after it streams straight through.
func (w *gzipResponseWriter) decide() error {
	w.decided = true
	if len(w.buf) > 0 && w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", http.DetectContentType(w.buf))
	}
	// Only a complete 200 body is safe to re-encode: a 304 has no body, and a
	// 206 is a byte range of the *uncompressed* file.
	if w.status == http.StatusOK && len(w.buf) >= gzipMinSize && compressible(w.Header().Get("Content-Type")) {
		w.gzipOn = true
		w.Header().Set("Content-Encoding", "gzip")
		// The uncompressed length no longer describes the body.
		w.Header().Del("Content-Length")
	}
	w.ResponseWriter.WriteHeader(w.status)
	if len(w.buf) == 0 {
		return nil
	}
	buf := w.buf
	w.buf = nil
	if !w.gzipOn {
		_, err := w.ResponseWriter.Write(buf)
		return err
	}
	w.gz = gzipWriterPool.Get().(*gzip.Writer)
	w.gz.Reset(w.ResponseWriter)
	_, err := w.gz.Write(buf)
	return err
}

// finish flushes a body that never reached the threshold and closes the gzip
// stream. It runs even when the handler wrote nothing at all.
func (w *gzipResponseWriter) finish() {
	if !w.decided {
		if w.status == 0 {
			w.status = http.StatusOK
		}
		_ = w.decide()
	}
	if w.gz != nil {
		_ = w.gz.Close()
		gzipWriterPool.Put(w.gz)
		w.gz = nil
	}
}

// Flush keeps streaming handlers working through the wrapper.
func (w *gzipResponseWriter) Flush() {
	if !w.decided {
		_ = w.decide()
	}
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
