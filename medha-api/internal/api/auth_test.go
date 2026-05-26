package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBearerAuth_AllowsGETWithoutToken(t *testing.T) {
	mw := BearerAuth("secret")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)
	if w.Code != 204 {
		t.Errorf("GET without auth = %d, want 204", w.Code)
	}
}

func TestBearerAuth_RejectsMutatingWithoutToken(t *testing.T) {
	mw := BearerAuth("secret")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte("{}")))
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("POST without auth = %d, want 401", w.Code)
	}
}

func TestBearerAuth_AcceptsCorrectToken(t *testing.T) {
	mw := BearerAuth("secret")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte("{}")))
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)
	if w.Code != 204 {
		t.Errorf("POST with auth = %d, want 204", w.Code)
	}
}

func TestBearerAuth_DisabledWhenSecretEmpty(t *testing.T) {
	mw := BearerAuth("")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte("{}")))
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)
	if w.Code != 204 {
		t.Errorf("empty secret should disable auth: status = %d", w.Code)
	}
}

func TestRateLimiter_AllowsBurst(t *testing.T) {
	rl := NewRateLimiter(3, time.Hour)
	mw := rl.Middleware()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "1.2.3.4:5678"
		w := httptest.NewRecorder()
		mw(next).ServeHTTP(w, req)
		if w.Code != 204 {
			t.Errorf("burst[%d] = %d", i, w.Code)
		}
	}
	// 4th request is denied.
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)
	if w.Code != 429 {
		t.Errorf("4th = %d, want 429", w.Code)
	}
}

func TestRateLimiter_PerKey(t *testing.T) {
	rl := NewRateLimiter(1, time.Hour)
	mw := rl.Middleware()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	// First IP gets one token.
	a := httptest.NewRequest(http.MethodGet, "/x", nil)
	a.RemoteAddr = "1.2.3.4:5678"
	wA := httptest.NewRecorder()
	mw(next).ServeHTTP(wA, a)
	if wA.Code != 204 {
		t.Errorf("A first = %d", wA.Code)
	}
	// Second IP is unaffected by A's bucket.
	b := httptest.NewRequest(http.MethodGet, "/x", nil)
	b.RemoteAddr = "5.6.7.8:9012"
	wB := httptest.NewRecorder()
	mw(next).ServeHTTP(wB, b)
	if wB.Code != 204 {
		t.Errorf("B first = %d", wB.Code)
	}
}

func TestRateLimiter_NilIsPassthrough(t *testing.T) {
	var rl *RateLimiter
	mw := rl.Middleware()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)
	if w.Code != 204 {
		t.Errorf("nil RateLimiter status = %d", w.Code)
	}
}
