package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidBearer(t *testing.T) {
	if !validBearer("Bearer this-is-a-long-control-token", "this-is-a-long-control-token") {
		t.Fatal("validBearer rejected a matching token")
	}
	if validBearer("Bearer wrong", "this-is-a-long-control-token") {
		t.Fatal("validBearer accepted the wrong token")
	}
}

func TestLandingPage(t *testing.T) {
	server, err := NewServer(Config{
		ControlToken:   "this-is-a-long-control-token",
		PublicURL:      "http://localhost:8080",
		InsecureCookie: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "Open a preview link") {
		t.Fatalf("landing page did not include instructions: %s", recorder.Body.String())
	}
}
