package wasi

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tetratelabs/wazero/api"
	"github.com/tinywasm/binary"
	"github.com/tinywasm/bus"
	"nhooyr.io/websocket"
)

// Empty valid WASM binary
var emptyWasm = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

func TestWasiServer_StartStop(t *testing.T) {
	// Find free port
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	srv := New()
	srv.SetPort(fmt.Sprintf("%d", port))
	// Avoid loading invalid files
	tmp, _ := os.MkdirTemp("", "wasi-empty")
	defer os.RemoveAll(tmp)
	srv.SetOutputDir(tmp)

	var wg sync.WaitGroup
	go srv.StartServer(&wg)
	waitForPort(t, port)

	// Stop
	srv.exitChan <- true
	wg.Wait()
}

func TestWasiServer_SwapModule_Lifecycle(t *testing.T) {
	srv := New()
	// Use temp dir
	tmp, _ := os.MkdirTemp("", "wasi-lifecycle")
	defer os.RemoveAll(tmp)
	srv.SetOutputDir(tmp)

	err := srv.swapModule("test", emptyWasm)
	if err != nil {
		t.Fatalf("swapModule failed: %v", err)
	}

	srv.mu.RLock()
	mod := srv.modules["test"]
	srv.mu.RUnlock()

	if mod == nil {
		t.Error("Module not loaded")
	}

	// Swap again (reload)
	err = srv.swapModule("test", emptyWasm)
	if err != nil {
		t.Fatalf("swapModule reload failed: %v", err)
	}
}

// Unit tests for HostBuilder
type mockMemory struct {
	data       []byte
	api.Memory // Embed to satisfy interface, will panic if unused methods called
}

func (m *mockMemory) Read(offset, count uint32) ([]byte, bool) {
	if int(offset)+int(count) > len(m.data) {
		return nil, false
	}
	// Return copy
	out := make([]byte, count)
	copy(out, m.data[offset:offset+count])
	return out, true
}

func (m *mockMemory) Write(offset uint32, v []byte) bool {
	if int(offset)+len(v) > len(m.data) {
		return false
	}
	copy(m.data[offset:], v)
	return true
}

type mockFunction struct {
	api.Function
	callFn func(ctx context.Context, params ...uint64) ([]uint64, error)
}

func (f *mockFunction) Call(ctx context.Context, params ...uint64) ([]uint64, error) {
	if f.callFn != nil {
		return f.callFn(ctx, params...)
	}
	return nil, nil
}

type mockModule struct {
	api.Module
	mem     *mockMemory
	exports map[string]api.Function
}

func (m *mockModule) Memory() api.Memory {
	return m.mem
}

func (m *mockModule) ExportedFunction(name string) api.Function {
	return m.exports[name]
}

func TestHostBuilder_Functions(t *testing.T) {
	// Setup
	b := bus.New()
	var receivedPayload atomic.Value

	b.Subscribe("test", func(msg binary.Message) {
		receivedPayload.Store(string(msg.Payload))
	})

	wsBroadcastCalled := false
	wsB := func(topic string, msg []byte) {
		if topic == "ws-topic" && string(msg) == "ws-payload" {
			wsBroadcastCalled = true
		}
	}

	loggerCalled := false
	logger := func(msg ...any) {
		loggerCalled = true
	}

	hb := NewHostBuilder(b, wsB, logger)

	// Create mock module with memory
	mem := &mockMemory{data: make([]byte, 1024)}
	// Write strings to memory
	// "test" at 0
	copy(mem.data[0:], "test")
	// "payload" at 10
	copy(mem.data[10:], "payload")

	// "ws-topic" at 20
	copy(mem.data[20:], "ws-topic")
	// "ws-payload" at 30
	copy(mem.data[30:], "ws-payload")

	// "log-msg" at 50
	copy(mem.data[50:], "log-msg")

	mod := &mockModule{
		mem:     mem,
		exports: make(map[string]api.Function),
	}

	ctx := context.Background()

	// Test publish
	// publish(topicPtr, topicLen, payloadPtr, payloadLen)
	hb.publish(ctx, mod, 0, 4, 10, 7)

	// Verify bus received
	// Bus might be async.
	time.Sleep(50 * time.Millisecond)
	if receivedPayload.Load() != "payload" {
		t.Errorf("Publish failed, got %s", receivedPayload.Load())
	}

	// Test ws_broadcast
	hb.wsBroadcastFunc(ctx, mod, 20, 8, 30, 10)
	if !wsBroadcastCalled {
		t.Error("ws_broadcast not called")
	}

	// Test log
	hb.log(ctx, mod, 50, 7)
	if !loggerCalled {
		t.Error("logger not called")
	}
}

func TestHostBuilder_Subscribe(t *testing.T) {
	b := bus.New()
	hb := NewHostBuilder(b, nil, nil)

	// Create module
	mem := &mockMemory{data: make([]byte, 1024)}
	mod := &mockModule{
		mem:     mem,
		exports: make(map[string]api.Function),
	}

	// "sub-topic" at 0
	copy(mem.data[0:], "sub-topic")

	// Mock exports
	var onMessageCalled atomic.Bool
	mod.exports = make(map[string]api.Function)
	mod.exports["on_message"] = &mockFunction{
		callFn: func(ctx context.Context, params ...uint64) ([]uint64, error) {
			onMessageCalled.Store(true)
			ptr := uint32(params[0])
			length := uint32(params[1])
			// Verify payload
			if string(mem.data[ptr:ptr+length]) != "hello" {
				t.Errorf("on_message received wrong payload: %s", mem.data[ptr:ptr+length])
			}
			return nil, nil
		},
	}

	mod.exports["malloc"] = &mockFunction{
		callFn: func(ctx context.Context, params ...uint64) ([]uint64, error) {
			// Return offset 100
			return []uint64{100}, nil
		},
	}

	// Setup context with Module
	realMod := &Module{
		cleanups: []func(){},
	}
	ctx := context.WithValue(context.Background(), moduleKey{}, realMod)

	// Call subscribe
	hb.subscribe(ctx, mod, 0, 9, 0)

	// Verify cleanup registered
	if len(realMod.cleanups) != 1 {
		t.Error("Cleanup not registered")
	}

	// Publish message to bus
	b.Publish("sub-topic", binary.Message{Payload: []byte("hello")})

	// Wait for callback
	time.Sleep(50 * time.Millisecond)

	if !onMessageCalled.Load() {
		t.Error("on_message not called")
	}

	// Verify cleanup works (unsubscribe)
	realMod.cleanups[0]()

	// Publish again, should not call on_message
	onMessageCalled.Store(false)
	b.Publish("sub-topic", binary.Message{Payload: []byte("hello")})
	time.Sleep(50 * time.Millisecond)
	if onMessageCalled.Load() {
		t.Error("on_message called after unsubscribe")
	}
}

func TestWsHub(t *testing.T) {
	// Setup hub
	b := bus.New()
	hub := &wsHub{
		clients: make(map[string]map[*wsConn]bool),
		bus:     b,
	}

	mux := http.NewServeMux()
	hub.RegisterRoute(mux)

	server := httptest.NewServer(mux)
	defer server.Close()

	// Connect WS
	u := "ws" + server.URL[4:] + "/ws?topic=test"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(websocket.StatusInternalError, "bye")

	// Broadcast
	hub.Broadcast("test", []byte("hello-ws"))

	// Read
	_, msg, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(msg) != "hello-ws" {
		t.Errorf("WS Expected 'hello-ws', got '%s'", msg)
	}
}

// Helpers
func waitForPort(t *testing.T, port int) {
	timeout := time.After(5 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			t.Fatal("Timeout waiting for server port")
		case <-ticker.C:
			conn, err := net.Dial("tcp", fmt.Sprintf(":%d", port))
			if err == nil {
				conn.Close()
				return
			}
		}
	}
}

func TestWasiServer_NewFileEvent(t *testing.T) {
	srv := New()
	tmp, _ := os.MkdirTemp("", "wasi-event")
	defer os.RemoveAll(tmp)
	srv.SetOutputDir(tmp)

	path := filepath.Join(tmp, "test.wasm")
	os.WriteFile(path, emptyWasm, 0644)

	err := srv.NewFileEvent("test.wasm", ".wasm", path, "write")
	if err != nil {
		t.Error(err)
	}

	srv.mu.RLock()
	mod := srv.modules["test"]
	srv.mu.RUnlock()
	if mod == nil {
		t.Error("Module not loaded")
	}
}

func TestWasiServer_RestartServer(t *testing.T) {
	srv := New()
	tmp, _ := os.MkdirTemp("", "wasi-restart")
	defer os.RemoveAll(tmp)
	srv.SetOutputDir(tmp)

	path := filepath.Join(tmp, "test.wasm")
	os.WriteFile(path, emptyWasm, 0644)

	// Pre-load
	srv.swapModule("test", emptyWasm)

	// Restart
	err := srv.RestartServer()
	if err != nil {
		t.Error(err)
	}

	// Verify loaded
	srv.mu.RLock()
	mod := srv.modules["test"]
	srv.mu.RUnlock()
	if mod == nil {
		t.Error("Module not loaded after restart")
	}
}
