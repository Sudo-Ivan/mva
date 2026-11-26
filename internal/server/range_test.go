package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseRangeFull(t *testing.T) {
	start, end, ok := parseRange("bytes=0-", 100)
	if !ok || start != 0 || end != 99 {
		t.Fatalf("got %d-%d ok=%v", start, end, ok)
	}
}

func TestParseRangeExplicit(t *testing.T) {
	start, end, ok := parseRange("bytes=10-19", 100)
	if !ok || start != 10 || end != 19 {
		t.Fatalf("got %d-%d ok=%v", start, end, ok)
	}
}

func TestParseRangeSuffix(t *testing.T) {
	start, end, ok := parseRange("bytes=-10", 100)
	if !ok || start != 90 || end != 99 {
		t.Fatalf("got %d-%d ok=%v", start, end, ok)
	}
}

func TestParseRangeInvalid(t *testing.T) {
	cases := []string{
		"invalid",
		"bytes=200-100",
		"bytes=500-",
		"bytes=0-200",
		"bytes=-9227000000000000000",
		"bytes=0-99999999999999999999",
		"bytes=99999999999999999999-",
	}
	for _, c := range cases {
		if _, _, ok := parseRange(c, 100); ok {
			t.Fatalf("expected invalid range for %q", c)
		}
	}
}

func TestServeBytesRange(t *testing.T) {
	rec := &recordingWriter{header: make(map[string][]string)}
	data := []byte("0123456789")
	n := serveBytes(rec, newRangeRequest("bytes=2-5"), data)
	if n != 4 {
		t.Fatalf("written = %d", n)
	}
	if string(rec.body) != "2345" {
		t.Fatalf("body = %q", rec.body)
	}
	if rec.status != 206 {
		t.Fatalf("status = %d", rec.status)
	}
}

func TestServeBytesFull(t *testing.T) {
	rec := &recordingWriter{header: make(map[string][]string)}
	data := []byte("hello")
	n := serveBytes(rec, newRangeRequest(""), data)
	if n != 5 || string(rec.body) != "hello" {
		t.Fatalf("n=%d body=%q", n, rec.body)
	}
}

type recordingWriter struct {
	header map[string][]string
	status int
	body   []byte
}

func (w *recordingWriter) Header() http.Header { return w.header }
func (w *recordingWriter) Write(p []byte) (int, error) {
	w.body = append(w.body, p...)
	return len(p), nil
}
func (w *recordingWriter) WriteHeader(status int) { w.status = status }

func newRangeRequest(rangeHeader string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if rangeHeader != "" {
		r.Header.Set("Range", rangeHeader)
	}
	return r
}

func FuzzParseRange(f *testing.F) {
	for _, tc := range []struct {
		header string
		size   int
	}{
		{"bytes=0-", 10},
		{"bytes=0-9", 10},
		{"bytes=-5", 10},
		{"bytes=5-4", 10},
		{"bytes=0-999999999999999999999999", 10},
		{"bytes=-999999999999999999999999", 10},
		{"bytes=", 10},
		{"bytes=1-2,3-4", 10},
		{"", 0},
	} {
		f.Add(tc.header, tc.size)
	}
	f.Fuzz(func(t *testing.T, header string, size int) {
		if size <= 0 {
			return
		}
		start, end, ok := parseRange(header, size)
		if !ok {
			return
		}
		if start < 0 || end < start || end >= size {
			t.Fatalf("parseRange(%q, %d) = %d, %d, true", header, size, start, end)
		}
	})
}
