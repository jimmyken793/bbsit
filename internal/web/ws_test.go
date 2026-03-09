package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kingyoung/bbsit/internal/deployer"
)

func TestHubBroadcastToSubscribed(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWS(w, r)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	sub := ClientMessage{Action: "subscribe", ProjectIDs: []string{"app1"}}
	conn.WriteJSON(sub)
	time.Sleep(50 * time.Millisecond)

	hub.OnEvent(deployer.Event{
		Type:      deployer.EventStepStart,
		ProjectID: "app1",
		Step:      "pull",
		Timestamp: time.Now(),
	})

	hub.OnEvent(deployer.Event{
		Type:      deployer.EventStepStart,
		ProjectID: "app2",
		Step:      "pull",
		Timestamp: time.Now(),
	})

	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var evt deployer.Event
	json.Unmarshal(msg, &evt)
	if evt.ProjectID != "app1" {
		t.Errorf("got project %q, want app1", evt.ProjectID)
	}
	if evt.Step != "pull" {
		t.Errorf("got step %q, want pull", evt.Step)
	}

	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_, _, err = conn.ReadMessage()
	if err == nil {
		t.Error("expected timeout for unsubscribed project, got message")
	}
}

func TestHubUnsubscribe(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWS(w, r)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.WriteJSON(ClientMessage{Action: "subscribe", ProjectIDs: []string{"app1"}})
	time.Sleep(50 * time.Millisecond)
	conn.WriteJSON(ClientMessage{Action: "unsubscribe", ProjectIDs: []string{"app1"}})
	time.Sleep(50 * time.Millisecond)

	hub.OnEvent(deployer.Event{Type: deployer.EventLog, ProjectID: "app1", Message: "test"})

	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_, _, err = conn.ReadMessage()
	if err == nil {
		t.Error("should not receive events after unsubscribe")
	}
}

func TestHubClientDisconnect(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWS(w, r)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	conn.WriteJSON(ClientMessage{Action: "subscribe", ProjectIDs: []string{"app1"}})
	time.Sleep(50 * time.Millisecond)

	conn.Close()
	time.Sleep(50 * time.Millisecond)

	hub.OnEvent(deployer.Event{Type: deployer.EventLog, ProjectID: "app1", Message: "after disconnect"})

	conn2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial after disconnect: %v", err)
	}
	conn2.Close()
}

func TestHubConcurrentEvents(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWS(w, r)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.WriteJSON(ClientMessage{Action: "subscribe", ProjectIDs: []string{"app1"}})
	time.Sleep(50 * time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hub.OnEvent(deployer.Event{Type: deployer.EventLog, ProjectID: "app1", Message: "concurrent"})
		}()
	}
	wg.Wait()

	conn.SetReadDeadline(time.Now().Add(time.Second))
	count := 0
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
		count++
	}
	if count == 0 {
		t.Error("expected to read at least one event")
	}
}
