package realdebrid

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"seanime/internal/debrid/debrid"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/samber/mo"
)

const testAPIKey = "test-token"

type capturedRequest struct {
	method      string
	path        string
	auth        string
	contentType string
	form        url.Values
}

func newTestProvider(t *testing.T, status int, body string) (*RealDebrid, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(raw))
		*captured = capturedRequest{
			method:      r.Method,
			path:        r.URL.Path,
			auth:        r.Header.Get("Authorization"),
			contentType: r.Header.Get("Content-Type"),
			form:        form,
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	logger := zerolog.Nop()
	p := NewRealDebridT(&logger)
	p.baseUrl = srv.URL
	p.apiKey = mo.Some(testAPIKey)
	return p, captured
}

func assertFormRequest(t *testing.T, got *capturedRequest, path string) {
	t.Helper()
	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if got.path != path {
		t.Errorf("path = %q, want %q", got.path, path)
	}
	if got.auth != "Bearer "+testAPIKey {
		t.Errorf("Authorization = %q, want bearer token", got.auth)
	}
	if got.contentType != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", got.contentType)
	}
	if mt, _, _ := mime.ParseMediaType(got.contentType); strings.HasPrefix(mt, "multipart/") {
		t.Errorf("request must not be multipart, got %q", mt)
	}
}

func TestAddMagnet(t *testing.T) {
	magnets := map[string]string{
		"simple": "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
		"special characters": "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567" +
			"&dn=Show+Name%20[Group]+100%25+A%26B=C&tr=udp%3A%2F%2Ftracker.example.org%3A1337%2Fannounce" +
			"&tr=https://tracker.example.org/announce?k=v&x=1",
	}
	for name, magnet := range magnets {
		t.Run(name, func(t *testing.T) {
			p, got := newTestProvider(t, http.StatusCreated, `{"id":"ABC123","uri":"https://api.real-debrid.com/rest/1.0/torrents/info/ABC123"}`)

			resp, err := p.addMagnet(magnet)
			if err != nil {
				t.Fatalf("addMagnet: %v", err)
			}
			assertFormRequest(t, got, "/torrents/addMagnet")
			if len(got.form) != 1 || got.form.Get("magnet") != magnet {
				t.Errorf("form = %v, want only magnet=%q", got.form, magnet)
			}
			if resp.ID != "ABC123" || !strings.HasSuffix(resp.URI, "/torrents/info/ABC123") {
				t.Errorf("unexpected response: %+v", resp)
			}
		})
	}
}

func TestSelectFiles(t *testing.T) {
	for _, files := range []string{"all", "1,3,12"} {
		t.Run(files, func(t *testing.T) {
			p, got := newTestProvider(t, http.StatusNoContent, "")

			if err := p._selectFiles("ABC123", files); err != nil {
				t.Fatalf("_selectFiles: %v", err)
			}
			assertFormRequest(t, got, "/torrents/selectFiles/ABC123")
			if len(got.form) != 1 || got.form.Get("files") != files {
				t.Errorf("form = %v, want only files=%q", got.form, files)
			}
		})
	}
}

func TestUnrestrictLink(t *testing.T) {
	link := "https://real-debrid.com/d/ABCDEF?name=a&b=c%20d#frag"
	p, got := newTestProvider(t, http.StatusOK, `{"id":"XYZ","filename":"ep01.mkv","filesize":1234,"link":"https://real-debrid.com/d/ABCDEF","download":"https://example.com/d/ep01.mkv","streamable":1}`)

	resp, err := p.unrestrictLink(link)
	if err != nil {
		t.Fatalf("unrestrictLink: %v", err)
	}
	assertFormRequest(t, got, "/unrestrict/link")
	if len(got.form) != 1 || got.form.Get("link") != link {
		t.Errorf("form = %v, want only link=%q", got.form, link)
	}
	if resp.ID != "XYZ" || resp.Filename != "ep01.mkv" || resp.Filesize != 1234 ||
		resp.Download != "https://example.com/d/ep01.mkv" || resp.Streamable != 1 {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestFormRequestsPropagateAPIError(t *testing.T) {
	const apiErr = `{"error":"bad_token","error_details":"Check parameters and/or cookies sent","error_code":9}`

	calls := map[string]func(p *RealDebrid) error{
		"addMagnet":      func(p *RealDebrid) error { _, err := p.addMagnet("magnet:?xt=urn:btih:abc"); return err },
		"_selectFiles":   func(p *RealDebrid) error { return p._selectFiles("ABC123", "all") },
		"unrestrictLink": func(p *RealDebrid) error { _, err := p.unrestrictLink("https://real-debrid.com/d/ABC"); return err },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			p, _ := newTestProvider(t, http.StatusForbidden, apiErr)

			err := call(p)
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range []string{"403 Forbidden", "Check parameters and/or cookies sent"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestQueryHTTPFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"blocked", 451, `{"error":"infringing_file","error_code":35}`, "infringing_file"},
		{"blocked empty body", 451, "", "choose a different release"},
		{"blocked HTML", 451, "<html>Unavailable</html>", "451"},
		{"error without numeric code", 403, `{"error":"permission_denied"}`, "permission_denied"},
		{"empty server error", 503, "", "503"},
		{"HTML server error", 502, "<html>Bad gateway</html>", "502"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := newTestProvider(t, tc.status, tc.body)
			_, err := p.addMagnet("magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if errors.Is(err, errFileBlocked) != (tc.status == 451) {
				t.Fatalf("blocked classification = %v, status = %d", err, tc.status)
			}
		})
	}
}

func TestStreamLinkErrors(t *testing.T) {
	for _, tc := range []struct {
		name         string
		blockedPath  string
		transient    bool
		wantRequests int32
	}{
		{"blocked torrent info", "/torrents/info/ABC", false, 1},
		{"blocked download link", "/unrestrict/link", false, 2},
		{"available file", "", false, 2},
		{"temporary link error recovers", "", true, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests, linkRequests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path == tc.blockedPath {
					w.WriteHeader(http.StatusUnavailableForLegalReasons)
					_, _ = io.WriteString(w, `{"error":"infringing_file","error_code":35}`)
					return
				}
				switch r.URL.Path {
				case "/torrents/info/ABC":
					_, _ = io.WriteString(w, `{"id":"ABC","status":"downloaded","files":[{"id":1,"path":"/ep16.mkv","selected":1}],"links":["https://real-debrid.com/d/ABC"]}`)
				case "/unrestrict/link":
					if linkRequests.Add(1) == 1 && tc.transient {
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = io.WriteString(w, `{"error":"temporarily_unavailable","error_code":20}`)
						return
					}
					_, _ = io.WriteString(w, `{"download":"https://example.com/ep16.mkv"}`)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			logger := zerolog.Nop()
			p := NewRealDebridT(&logger)
			p.baseUrl = srv.URL
			p.apiKey = mo.Some(testAPIKey)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stream, err := p.GetTorrentStreamUrl(ctx, debrid.StreamTorrentOptions{ID: "ABC", FileId: "1"}, nil)
			if tc.blockedPath != "" {
				if !errors.Is(err, errFileBlocked) || stream != "" {
					t.Fatalf("stream = %q, error = %v; want immediate blocked error", stream, err)
				}
			} else if err != nil || stream != "https://example.com/ep16.mkv" {
				t.Fatalf("stream = %q, error = %v; want playable URL", stream, err)
			}
			if got := requests.Load(); got != tc.wantRequests {
				t.Errorf("requests = %d, want %d", got, tc.wantRequests)
			}
		})
	}
}
