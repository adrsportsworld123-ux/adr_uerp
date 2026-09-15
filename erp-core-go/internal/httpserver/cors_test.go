package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Reproduces the exact bug reported against the running server: a route
// registered only for POST (chi's api.Post("/auth/login", ...), no OPTIONS
// route) returns 405 for a browser's CORS preflight OPTIONS request,
// blocking every browser-based client (Flutter web, or a future web admin)
// before a single request reaches auth or business logic.
func innerHandlerWithNoOptionsRoute() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func TestCORS_withoutMiddleware_reproducesThe405(t *testing.T) {
	srv := httptest.NewServer(innerHandlerWithNoOptionsRoute())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/api/v1/auth/login", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 (reproducing the reported bug), got %d", resp.StatusCode)
	}
}

func TestCORS_preflightSucceedsAndRealRequestStillWorks(t *testing.T) {
	srv := httptest.NewServer(CORS(innerHandlerWithNoOptionsRoute()))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/api/v1/auth/login", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 from CORS intercepting the preflight, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected Access-Control-Allow-Origin: *, got %q", got)
	}

	postResp, err := http.Post(srv.URL+"/api/v1/auth/login", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if postResp.StatusCode != http.StatusOK {
		t.Fatalf("expected the real POST to still reach the handler, got %d", postResp.StatusCode)
	}
}
