package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"argus/internal/store"
)

type Hub struct {
	mu      sync.Mutex
	clients map[*hubClient]struct{}
}

// Keepalive. Proxies close a WebSocket that carries nothing for a while
// (nginx after 60 seconds by default, Cloudflare after 100), and with a few
// monitors the stream can be quiet for longer than that. A ping every 25
// seconds keeps it open; a client that stops answering for pongWait is gone.
var (
	pingInterval = 25 * time.Second
	pongWait     = 60 * time.Second
)

type hubClient struct {
	conn *websocket.Conn
	send chan []byte
}

func NewHub() *Hub {
	return &Hub{clients: map[*hubClient]struct{}{}}
}

func (h *Hub) Broadcast(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.send <- msg:
		default:
			go h.remove(c)
		}
	}
}

func (h *Hub) remove(c *hubClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
	}
}

func (h *Hub) Add(conn *websocket.Conn) {
	c := &hubClient{conn: conn, send: make(chan []byte, 64)}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	go h.writePump(c)
	go h.readPump(c)
}

// readPump reads so that pongs and close frames are handled (gorilla only
// processes control frames while reading), and so a client that has gone
// away is noticed when its pongs stop.
func (h *Hub) readPump(c *hubClient) {
	defer h.remove(c)
	c.conn.SetReadLimit(512)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (h *Hub) writePump(c *hubClient) {
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	defer h.remove(c)
	defer func() { _ = c.conn.Close() }()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ping.C:
			if err := c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
				return
			}
		}
	}
}

func (h *Hub) Run(ctx context.Context, st *store.Store) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		err := st.Listen(ctx, store.ChannelEvents, func(payload string) {
			h.Broadcast([]byte(payload))
		})
		if ctx.Err() != nil {
			return
		}
		slog.Warn("events listener dropped, retrying", "error", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  512,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		originHost, _, err := net.SplitHostPort(u.Host)
		if err != nil {
			originHost = u.Host
		}
		reqHost, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			reqHost = r.Host
		}
		return originHost == reqHost
	},
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.hub.Add(conn)
}
