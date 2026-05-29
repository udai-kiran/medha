package viewer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestHub_PublishToMultipleSubscribers(t *testing.T) {
	hub := NewHub(nil)
	c1, u1 := hub.Subscribe()
	c2, u2 := hub.Subscribe()
	defer u1()
	defer u2()

	hub.Publish(Event{Type: "observation", SessionID: "sess-1", ID: "obs-1"})

	timeout := time.After(time.Second)
	for _, ch := range []<-chan Event{c1, c2} {
		select {
		case evt := <-ch:
			if evt.ID != "obs-1" {
				t.Errorf("got %+v", evt)
			}
		case <-timeout:
			t.Fatal("subscriber didn't receive event")
		}
	}
}

func TestHub_SlowSubscriberDropped(t *testing.T) {
	hub := NewHub(nil)
	hub.ClientBuffer = 2

	_, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	// Publish many events without reading — should not block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			hub.Publish(Event{Type: "observation", ID: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on slow subscriber")
	}
}

func TestHub_MaxClientsRespected(t *testing.T) {
	hub := NewHub(nil)
	hub.MaxClients = 2

	c1, u1 := hub.Subscribe()
	c2, u2 := hub.Subscribe()
	defer u1()
	defer u2()
	// 3rd should immediately get a closed channel.
	c3, u3 := hub.Subscribe()
	defer u3()
	_, ok := <-c3
	if ok {
		t.Error("third subscriber should be rejected (channel closed)")
	}
	_ = c1
	_ = c2
}

func TestHub_BroadcastObservation(t *testing.T) {
	hub := NewHub(nil)
	c, u := hub.Subscribe()
	defer u()

	hub.BroadcastObservation(context.Background(), "sess-1", "obs-1", "")
	select {
	case evt := <-c:
		if evt.Type != "observation" || evt.SessionID != "sess-1" || evt.ID != "obs-1" {
			t.Errorf("got %+v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
}

func TestHandler_DashboardServes(t *testing.T) {
	h := New(NewHub(nil), nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "agent_mem") {
		t.Error("dashboard HTML missing branding")
	}
}

func TestHandler_HealthSubscriberCount(t *testing.T) {
	hub := NewHub(nil)
	_, u := hub.Subscribe()
	defer u()
	h := New(hub, nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), `"subscribers":1`) {
		t.Errorf("health body = %q", w.Body.String())
	}
}

func TestHandler_StreamWebSocket(t *testing.T) {
	hub := NewHub(nil)
	srv := httptest.NewServer(New(hub, nil))
	defer srv.Close()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/stream"
	c, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		resp.Body.Close()
	}
	defer func() { _ = c.Close() }()

	// First message is the hello frame.
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	var hello Event
	if err := c.ReadJSON(&hello); err != nil {
		t.Fatalf("hello: %v", err)
	}
	if hello.Type != "system" {
		t.Errorf("hello type = %q", hello.Type)
	}

	// Publish + receive.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		var evt Event
		if err := c.ReadJSON(&evt); err != nil {
			t.Errorf("read: %v", err)
			return
		}
		if evt.Type != "observation" || evt.ID != "obs-x" {
			t.Errorf("got %+v", evt)
		}
	}()
	time.Sleep(100 * time.Millisecond) // give server time to register subscriber
	hub.Publish(Event{Type: "observation", ID: "obs-x"})
	wg.Wait()
}
