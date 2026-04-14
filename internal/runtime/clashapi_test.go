package runtime_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"proxypools/internal/runtime"
)

func TestSwitchSelectorCallsClashAPI(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/proxies/active-http" {
			called = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := runtime.NewClashAPI(server.URL, "")
	if err := client.SwitchSelector("active-http", "node-1"); err != nil {
		t.Fatalf("switch failed: %v", err)
	}
	if !called {
		t.Fatal("expected selector switch request")
	}
}
