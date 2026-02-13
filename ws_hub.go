package wasi

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/tinywasm/bus"
	"nhooyr.io/websocket"
)

type wsConn struct {
	conn *websocket.Conn
	send chan []byte
}

type wsHub struct {
	clients map[string]map[*wsConn]bool
	mu      sync.RWMutex
	bus     bus.Bus
}

func (h *wsHub) RegisterRoute(mux *http.ServeMux) {
	mux.HandleFunc("/ws", h.handleWS)
}

func (h *wsHub) Broadcast(topic string, msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	clients, ok := h.clients[topic]
	if !ok {
		return
	}

	for client := range clients {
		select {
		case client.send <- msg:
		default:
			// Buffer full, drop message
		}
	}
}

func (h *wsHub) handleWS(w http.ResponseWriter, r *http.Request) {
	topic := r.URL.Query().Get("topic")
	if topic == "" {
		http.Error(w, "topic required", http.StatusBadRequest)
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Allow cross-origin for dev
	})
	if err != nil {
		return
	}

	conn := &wsConn{
		conn: c,
		send: make(chan []byte, 256),
	}

	h.register(topic, conn)

	// Start write pump
	go conn.writePump()

	// Read loop to handle close frames
	defer func() {
		h.unregister(topic, conn)
		c.Close(websocket.StatusNormalClosure, "")
	}()

	for {
		_, _, err := c.Read(context.Background())
		if err != nil {
			break
		}
	}
}

func (c *wsConn) writePump() {
	for msg := range c.send {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := c.conn.Write(ctx, websocket.MessageBinary, msg)
		cancel()
		if err != nil {
			return
		}
	}
}

func (h *wsHub) register(topic string, conn *wsConn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.clients == nil {
		h.clients = make(map[string]map[*wsConn]bool)
	}
	if h.clients[topic] == nil {
		h.clients[topic] = make(map[*wsConn]bool)
	}
	h.clients[topic][conn] = true
}

func (h *wsHub) unregister(topic string, conn *wsConn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if clients, ok := h.clients[topic]; ok {
		delete(clients, conn)
		if len(clients) == 0 {
			delete(h.clients, topic)
		}
	}
	// close channel to stop writePump?
	// But writePump might be writing.
	// We can't close channel if multiple writers (Broadcast).
	// But Broadcast is the only writer? Yes.
	// But unregister is called when Read fails or connection closes.
	// Broadcast might try to send to closed channel if we close it here?
	// If unregister is called, we remove from map.
	// Broadcast iterates map under lock.
	// So subsequent Broadcasts won't find it.
	// But concurrent Broadcast might have retrieved the client before unregister acquired lock.
	// So we should not close the channel, let the GC handle it, or use a closing signal.
	// Actually, if connection is closed, Write will fail, writePump will return.
}
