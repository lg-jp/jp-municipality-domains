package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// cloudFrontBlockBody mirrors the body CloudFront serves when AWS WAF rejects a
// request at the edge, as captured from a GitHub Actions runner.
const cloudFrontBlockBody = `<!DOCTYPE HTML PUBLIC "-//W3C//DTD HTML 4.01 Transitional//EN">
<HTML><HEAD><TITLE>ERROR: The request could not be satisfied</TITLE></HEAD><BODY>
<H1>403 ERROR</H1><H2>The request could not be satisfied.</H2>
Request blocked.
</BODY></HTML>`

func TestFetchOnceEdgeBlockedByCloudFront(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "CloudFront")
		w.Header().Set("X-Cache", "Error from cloudfront")
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, cloudFrontBlockBody)
	}))
	defer srv.Close()

	err := fetchOnce(context.Background(), discardLogger(), srv.URL)
	if !errors.Is(err, errEdgeBlocked) {
		t.Fatalf("want errEdgeBlocked, got %v", err)
	}
}

func TestFetchOnceOriginForbiddenIsRealFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "nginx/1.16.1")
		w.Header().Set("X-Cache", "Miss from cloudfront")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	err := fetchOnce(context.Background(), discardLogger(), srv.URL)
	if err == nil {
		t.Fatal("want error for an origin-served 403, got nil")
	}
	if errors.Is(err, errEdgeBlocked) {
		t.Fatalf("origin-served 403 must not be classified as an edge block: %v", err)
	}
}

func TestFetchOnceSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	if err := fetchOnce(context.Background(), discardLogger(), srv.URL); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

// An edge block is decided by source IP, so retrying can only ever repeat it.
func TestMonitorURLDoesNotRetryEdgeBlock(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Server", "CloudFront")
		w.Header().Set("X-Cache", "Error from cloudfront")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	err := monitorURL(context.Background(), discardLogger(), srv.URL)
	if !errors.Is(err, errEdgeBlocked) {
		t.Fatalf("want errEdgeBlocked, got %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("want exactly 1 request, got %d", n)
	}
}

func TestMonitorURLRetriesRealFailures(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if err := monitorURL(context.Background(), discardLogger(), srv.URL); err == nil {
		t.Fatal("want error, got nil")
	}
	if n := hits.Load(); n != maxRetries {
		t.Fatalf("want %d attempts, got %d", maxRetries, n)
	}
}
