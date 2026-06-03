package fetcher

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGet_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	body, err := get(srv.URL)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("unexpected body: %s", string(body))
	}
}

func TestGet_Retry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`ok`))
	}))
	defer srv.Close()

	// Temporarily shorten retry sleep for test speed
	body, err := get(srv.URL)
	if err != nil {
		t.Fatalf("expected success on 3rd try, got: %v", err)
	}
	if string(body) != "ok" {
		t.Fatalf("unexpected body: %s", string(body))
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestGet_AllFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	_, err := get(srv.URL)
	if err == nil {
		t.Fatal("expected error after all retries fail")
	}
}

func TestWorkerPool(t *testing.T) {
	codes := []string{"a", "b", "c", "d", "e"}
	var processed []string
	ch := make(chan string, len(codes))

	errs := workerPool(codes, 2, 0, func(code string) (struct{}, error) {
		ch <- code
		return struct{}{}, nil
	})

	close(ch)
	for c := range ch {
		processed = append(processed, c)
	}

	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	if len(processed) != len(codes) {
		t.Fatalf("expected %d processed, got %d", len(codes), len(processed))
	}
}
