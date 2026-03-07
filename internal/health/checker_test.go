package health

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kingyoung/bbsit/internal/types"
)

func TestCheck_None(t *testing.T) {
	if err := Check(types.HealthNone, "", time.Second, 1); err != nil {
		t.Fatalf("HealthNone should return nil, got: %v", err)
	}
}

func TestCheck_UnknownType(t *testing.T) {
	err := Check("bogus", "", time.Second, 1)
	if err == nil {
		t.Fatal("expected error for unknown health type")
	}
}

func TestCheck_HTTP_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := Check(types.HealthHTTP, srv.URL, 5*time.Second, 3); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestCheck_HTTP_FailStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := Check(types.HealthHTTP, srv.URL, time.Second, 1)
	if err == nil {
		t.Fatal("expected error for 500 status")
	}
}

func TestCheck_HTTP_Redirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	// 302 is in the 200-399 range, should pass
	if err := Check(types.HealthHTTP, srv.URL, 5*time.Second, 1); err != nil {
		t.Fatalf("expected redirect to pass, got: %v", err)
	}
}

func TestCheck_TCP_Success(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not start listener: %v", err)
	}
	defer ln.Close()

	if err := Check(types.HealthTCP, ln.Addr().String(), 5*time.Second, 3); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestCheck_TCP_Fail(t *testing.T) {
	// Use a port that's not listening
	err := Check(types.HealthTCP, "127.0.0.1:1", time.Second, 1)
	if err == nil {
		t.Fatal("expected error for closed port")
	}
}

func TestCheck_HTTP_RetryThenSuccess(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := Check(types.HealthHTTP, srv.URL, 3*time.Second, 5)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if attempts < 3 {
		t.Fatalf("expected at least 3 attempts, got %d", attempts)
	}
}

func TestCheck_HTTP_AllRetriesFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	err := Check(types.HealthHTTP, srv.URL, time.Second, 2)
	if err == nil {
		t.Fatal("expected error when all retries fail")
	}
}

func TestCheck_TCP_RetryThenSuccess(t *testing.T) {
	// Start listener after a brief delay
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not start listener: %v", err)
	}
	addr := ln.Addr().String()

	// Close and reopen to simulate service coming up on retry
	ln.Close()
	go func() {
		time.Sleep(200 * time.Millisecond)
		newLn, _ := net.Listen("tcp", addr)
		if newLn != nil {
			defer newLn.Close()
			time.Sleep(5 * time.Second)
		}
	}()

	err = Check(types.HealthTCP, addr, 3*time.Second, 5)
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
}

func TestCheck_HTTP_ConnectionRefused(t *testing.T) {
	// Use a URL that will refuse connections
	err := Check(types.HealthHTTP, "http://127.0.0.1:1/health", time.Second, 1)
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
}
