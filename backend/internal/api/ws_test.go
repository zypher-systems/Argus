package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func (h *Hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// TestWebSocketKeepalive is the flicker: with nothing to send, the stream
// went quiet and a proxy closed it. Pings keep it open, and a client that
// stops answering them is dropped instead of lingering.
func TestWebSocketKeepalive(t *testing.T) {
	prevPing, prevPong := pingInterval, pongWait
	pingInterval, pongWait = 40*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { pingInterval, pongWait = prevPing, prevPong })

	hub := NewHub()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.Add(conn)
	}))
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	// A live browser: reads, so it answers pings with pongs.
	live, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = live.Close() }()
	var pings atomic.Int32
	live.SetPingHandler(func(data string) error {
		pings.Add(1)
		return live.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
	})
	got := make(chan string, 4)
	go func() {
		for {
			_, msg, err := live.ReadMessage()
			if err != nil {
				return
			}
			got <- string(msg)
		}
	}()

	// A dead tab: connected, but never reads, so never answers.
	dead, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dead.Close() }()

	waitFor := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if ok() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", what)
	}
	waitFor("both clients", func() bool { return hub.count() == 2 })
	waitFor("pings", func() bool { return pings.Load() >= 3 })
	// Well past pongWait: the dead client is gone, the live one is not.
	waitFor("the dead client to be dropped", func() bool { return hub.count() == 1 })
	time.Sleep(3 * pongWait)
	if hub.count() != 1 {
		t.Fatalf("the answering client should stay connected, %d connected", hub.count())
	}
	hub.Broadcast([]byte(`{"type":"result"}`))
	select {
	case msg := <-got:
		if msg != `{"type":"result"}` {
			t.Fatalf("got %q", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("the kept-alive connection should still deliver results")
	}
}
