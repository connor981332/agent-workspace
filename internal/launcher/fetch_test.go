package launcher

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchRidesOutServerFailures(t *testing.T) {
	retryDelay = time.Millisecond
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer s.Close()
	got, err := fetch(s.URL, "checksums.txt")
	if err != nil || string(got) != "ok" || calls != 3 {
		t.Fatalf("got %q, %v after %d calls", got, err, calls)
	}
}

func TestFetchDoesNotRetryMissingFile(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer s.Close()
	if _, err := fetch(s.URL, "checksums.txt"); err == nil || calls != 1 {
		t.Fatalf("err %v after %d calls", err, calls)
	}
}
