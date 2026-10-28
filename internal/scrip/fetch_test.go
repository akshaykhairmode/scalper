package scrip

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/akshaykhairmode/scalper/internal/errs"
)

func TestClientListFiles(t *testing.T) {
	var gotAuth, gotAccept, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"filesPaths":["https://example.test/nse_fo.csv","https://example.test/bse_cm.csv"],"baseFolder":"https://example.test"}}`))
	}))
	defer srv.Close()

	c := NewClient("access-token")
	c.HTTP = srv.Client()
	list, err := c.ListFiles(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if gotAuth != "access-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotAccept != "application/json" {
		t.Fatalf("Accept = %q", gotAccept)
	}
	if gotPath != FilePathsPath {
		t.Fatalf("path = %q, want %q", gotPath, FilePathsPath)
	}
	if len(list.Paths) != 2 || list.BaseFolder != "https://example.test" {
		t.Fatalf("list = %+v", list)
	}
}

func TestClientListFilesErrors(t *testing.T) {
	t.Run("missing token", func(t *testing.T) {
		c := NewClient("")
		_, err := c.ListFiles(context.Background(), "https://example.test")
		if !errors.Is(err, errs.ErrNotLoggedIn) {
			t.Fatalf("err = %v, want ErrNotLoggedIn", err)
		}
	})
	t.Run("nil http", func(t *testing.T) {
		c := &Client{}
		if _, err := c.ListFiles(context.Background(), "https://example.test"); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("bad base url", func(t *testing.T) {
		c := NewClient("t")
		if _, err := c.ListFiles(context.Background(), "not-a-url"); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("http 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
		}))
		defer srv.Close()
		c := NewClient("t")
		c.HTTP = srv.Client()
		_, err := c.ListFiles(context.Background(), srv.URL)
		var be *errs.BrokerError
		if !errors.As(err, &be) {
			t.Fatalf("err = %T %v", err, err)
		}
		if !be.Retryable || be.StatusCode != http.StatusInternalServerError || !strings.Contains(be.Message, "boom") {
			t.Fatalf("broker error = %+v", be)
		}
	})
	t.Run("http 401", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()
		c := NewClient("t")
		c.HTTP = srv.Client()
		_, err := c.ListFiles(context.Background(), srv.URL)
		var be *errs.BrokerError
		if !errors.As(err, &be) || be.Retryable {
			t.Fatalf("err = %+v", err)
		}
	})
	t.Run("empty list", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"data":{"filesPaths":[]}}`))
		}))
		defer srv.Close()
		c := NewClient("t")
		c.HTTP = srv.Client()
		_, err := c.ListFiles(context.Background(), srv.URL)
		if err == nil || !strings.Contains(err.Error(), "no files") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("bad json", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{`))
		}))
		defer srv.Close()
		c := NewClient("t")
		c.HTTP = srv.Client()
		_, err := c.ListFiles(context.Background(), srv.URL)
		if err == nil || !strings.Contains(err.Error(), "decode") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("invalid file url", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"data":{"filesPaths":["://bad"]}}`))
		}))
		defer srv.Close()
		c := NewClient("t")
		c.HTTP = srv.Client()
		_, err := c.ListFiles(context.Background(), srv.URL)
		if err == nil || !strings.Contains(err.Error(), "invalid file path") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestClientDownload(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/nse_fo.csv" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("pSymbol\n1\n"))
	}))
	defer srv.Close()

	c := NewClient("secret-token")
	c.HTTP = srv.Client()
	data, err := c.Download(context.Background(), srv.URL+"/nse_fo.csv")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(data) != "pSymbol\n1\n" {
		t.Fatalf("data = %q", data)
	}
	if gotAuth != "" {
		t.Fatalf("download leaked Authorization: %q", gotAuth)
	}

	if _, err := c.Download(context.Background(), srv.URL+"/missing.csv"); err == nil {
		t.Fatal("expected 404 error")
	}
	for _, bad := range []string{"", "ftp://example.test/x.csv", "not a url", "https://"} {
		if _, err := c.Download(context.Background(), bad); err == nil {
			t.Fatalf("Download(%q) succeeded", bad)
		}
	}
	if _, err := (&Client{}).Download(context.Background(), srv.URL); err == nil {
		t.Fatal("nil http should fail")
	}
}

func TestClientDownloadEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	c := NewClient("t")
	c.HTTP = srv.Client()
	_, err := c.Download(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("err = %v", err)
	}
}

func TestSegmentForURL(t *testing.T) {
	cases := map[string]string{
		"https://x/prod/2025-01-22/transformed/nse_fo.csv":       SegmentNSEFO,
		"https://x/prod/2025-01-22/transformed/bse_fo.csv":       SegmentBSEFO,
		"https://x/prod/2025-01-22/transformed/nse_cm.csv":       SegmentNSECM,
		"https://x/prod/2025-01-22/transformed-v1/nse_cm-v1.csv": SegmentNSECM,
		"https://x/prod/2025-01-22/transformed/nse_com.csv":      SegmentNSECM,
		"https://x/prod/2025-01-22/transformed/bse_cm.csv":       SegmentBSECM,
		"https://x/prod/2025-01-22/transformed/cde_fo.csv":       "",
		"https://x/prod/2025-01-22/transformed/mcx_fo.csv":       SegmentMCXFO,
		"http://x/nse_fo-v1.csv":                                 SegmentNSEFO,
	}
	for in, want := range cases {
		got, ok := SegmentForURL(in)
		if got != want {
			t.Errorf("SegmentForURL(%q) = %q, %v; want %q", in, got, ok, want)
		}
		if (want == "") == ok {
			t.Errorf("SegmentForURL(%q) ok = %v", in, ok)
		}
	}
}

func TestSelectFiles(t *testing.T) {
	paths := []string{
		"https://x/nse_com.csv",
		"https://x/nse_cm-v1.csv",
		"https://x/nse_cm.csv",
		"https://x/bse_fo.csv",
		"https://x/mcx_fo.csv",
	}
	_, err := SelectFiles(paths, []string{SegmentNSEFO, SegmentBSEFO, SegmentNSECM, SegmentBSECM})
	if err == nil {
		t.Fatal("missing bse_cm should fail")
	}
	got, err := SelectFiles(paths, []string{SegmentNSECM, SegmentBSEFO})
	if err != nil {
		t.Fatalf("SelectFiles: %v", err)
	}
	if got[SegmentNSECM] != "https://x/nse_cm.csv" {
		t.Fatalf("nse_cm = %q", got[SegmentNSECM])
	}
	if got[SegmentBSEFO] != "https://x/bse_fo.csv" {
		t.Fatalf("bse_fo = %q", got[SegmentBSEFO])
	}
}

func TestJoinURL(t *testing.T) {
	cases := []struct{ base, want string }{
		{"https://example.test", "https://example.test" + FilePathsPath},
		{"https://example.test/", "https://example.test" + FilePathsPath},
		{"https://example.test/api/", "https://example.test/api" + FilePathsPath},
	}
	for _, tc := range cases {
		got, err := joinURL(tc.base, FilePathsPath)
		if err != nil || got != tc.want {
			t.Errorf("joinURL(%q) = %q, %v; want %q", tc.base, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "mailto:x", "http://", "://bad"} {
		if _, err := joinURL(bad, FilePathsPath); err == nil {
			t.Errorf("joinURL(%q) succeeded", bad)
		}
	}
}
