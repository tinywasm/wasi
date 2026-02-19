# Base C: tinywasm/wasi — Standalone Server

> **Goal**: `tinywasm/wasi` becomes a full HTTP server that replaces `tinywasm/server`
> entirely when `TINYWASM_SERVER=wasi`. Constructor takes zero args; all config via
> Set* methods with sensible defaults.

---

## Package Structure

```
wasi/
├── wasi.go       ← WasiServer + New() + Set* methods + ServerInterface impl
├── module.go     ← Module lifecycle (load/drain/init/close) via wazero
├── host.go       ← HostBuilder (host functions: publish/subscribe/ws_broadcast/log)
├── ws_hub.go     ← wsHub (WebSocket relay, registers /ws?topic= route)
└── docs/
    ├── ARCHITECTURE.md
    ├── WASI_SERVER.md      ← this file
    └── HANDOFF_PROTOCOL.md
```

---

## `wasi/wasi.go` — WasiServer API

### Constructor and defaults

```go
package wasi

// New creates a WasiServer with all defaults. Configure via Set* methods.
func New() *WasiServer

// Defaults:
//   AppRootDir   → "."
//   ModulesDir   → "modules"
//   OutputDir    → "modules/dist"
//   AppPort      → "6060"
//   DrainTimeout → 5s
//   ExitChan     → internal make(chan bool)
//   Logger       → noop
//   UI           → noop
//   Bus          → auto-created (tinywasm/bus)
```

### Set* methods (all return *WasiServer for optional chaining)

```go
func (s *WasiServer) SetAppRootDir(dir string) *WasiServer
func (s *WasiServer) SetModulesDir(dir string) *WasiServer
func (s *WasiServer) SetOutputDir(dir string) *WasiServer
func (s *WasiServer) SetPort(port string) *WasiServer
func (s *WasiServer) SetDrainTimeout(d time.Duration) *WasiServer
func (s *WasiServer) SetLogger(fn func(msg ...any)) *WasiServer
func (s *WasiServer) SetExitChan(ch chan bool) *WasiServer
func (s *WasiServer) SetUI(ui interface{ RefreshUI() }) *WasiServer
func (s *WasiServer) SetBus(b bus.Bus) *WasiServer
```

### Route registration

```go
// RegisterRoutes appends fn to the internal route list.
// Called before StartServer; matching the assetmin pattern.
func (s *WasiServer) RegisterRoutes(fn func(*http.ServeMux))
```

### Usage examples

```go
// Minimal (test):
srv := wasi.New()
srv.RegisterRoutes(myHandler.RegisterRoutes)
srv.StartServer(nil)

// Full (production via app factory):
srv := wasi.New()
srv.SetAppRootDir(startDir).
    SetLogger(logger.Logger).
    SetExitChan(exitChan).
    SetUI(ui)
// Routes registered later from InitBuildHandlers:
srv.RegisterRoutes(assets.RegisterRoutes)
srv.RegisterRoutes(client.RegisterRoutes)
```

---

## WasiServer struct (internal)

```go
type WasiServer struct {
    appRootDir   string
    modulesDir   string
    outputDir    string
    port         string
    drainTimeout time.Duration
    routes       []func(*http.ServeMux)
    bus          bus.Bus
    exitChan     chan bool
    logger       func(...any)
    ui           interface{ RefreshUI() }

    mux     *http.ServeMux
    httpSrv *http.Server
    modules map[string]*Module
    mu      sync.RWMutex
    wsHub   *wsHub
    watcher *fsnotify.Watcher
}
```

---

## ServerInterface Implementation

### `StartServer(wg *sync.WaitGroup)`

```
1. Build mux: register s.routes + wsHub.RegisterRoute
2. Load all *.wasm from outputDir → loadModule(name, bytes)
3. Start fsnotify watcher on outputDir
4. http.ListenAndServe(port, mux) in goroutine
5. Block on exitChan → StopServer()
6. wg.Done() on exit
```

### `StopServer() error`

```
1. Stop watcher
2. For each module: Drain(ctx, drainTimeout) → Close(ctx)
3. httpSrv.Shutdown(ctx)
```

### `RestartServer() error`

```
Hot-reload all modules. HTTP stays running (no port drop).
```

### `NewFileEvent(fileName, extension, filePath, event string) error`

```
if event == "write" && extension == ".wasm":
    name := strings.TrimSuffix(fileName, ".wasm")
    bytes := os.ReadFile(filePath)
    return s.swapModule(name, bytes)
```

### `UnobservedFiles() []string`

```go
return []string{filepath.Join(s.outputDir, "*.wasm")}
```

### `SupportedExtensions() []string`

```go
return []string{".wasm", ".go"}
```

### TUI methods

```go
func (s *WasiServer) Name() string        { return "WASI Server" }
func (s *WasiServer) Label() string       { return "Server Mode" }
func (s *WasiServer) Value() string       { return "wasi" }
func (s *WasiServer) Change(string) error { return nil }
func (s *WasiServer) RefreshUI()          { s.ui.RefreshUI() }
```

---

## `wasi/module.go` — Module Lifecycle

```go
type Module struct {
    name    string
    runtime wazero.Runtime
    mod     api.Module
    active  atomic.Int32
    drainFn api.Function  // exported drain() uint32
    initFn  api.Function  // exported init()
}

func Load(ctx context.Context, name string, wasmBytes []byte, hb *HostBuilder) (*Module, error)
func (m *Module) Drain(ctx context.Context, timeout time.Duration) error
func (m *Module) Init(ctx context.Context) error
func (m *Module) Close(ctx context.Context) error
```

---

## `wasi/host.go` — HostBuilder

```go
type HostBuilder struct {
    bus         bus.Bus
    wsBroadcast func(topic string, msg []byte)
}

func NewHostBuilder(b bus.Bus, wsBroadcast func(topic string, msg []byte)) *HostBuilder

// Build registers into wazero.Runtime the host functions:
//   publish(topic_ptr, topic_len, payload_ptr, payload_len)
//   subscribe(topic_ptr, topic_len, handler_fn_idx)
//   ws_broadcast(topic_ptr, topic_len, payload_ptr, payload_len)
//   log(msg_ptr, msg_len)
// And expects the module to export:
//   drain() uint32
//   init()
//   on_message(payload_ptr, payload_len)
func (h *HostBuilder) Build(rt wazero.Runtime) wazero.HostModuleBuilder
```

---

## `wasi/ws_hub.go` — WebSocket Relay

```go
type wsHub struct {
    clients map[string]map[*wsConn]bool
    mu      sync.RWMutex
    bus     bus.Bus
}

func (h *wsHub) RegisterRoute(mux *http.ServeMux)      // GET /ws?topic=
func (h *wsHub) Broadcast(topic string, msg []byte)
```

---

## `wasi/go.mod` — Dependencies

```bash
go get github.com/tetratelabs/wazero@latest
go get github.com/tinywasm/bus@latest
go get github.com/tinywasm/binary@latest
go get github.com/fsnotify/fsnotify@latest
```

---

## Compile-time assertion

```go
// In wasi/wasi.go
var _ serverInterface = (*WasiServer)(nil)

type serverInterface interface {
    StartServer(wg *sync.WaitGroup)
    StopServer() error
    RestartServer() error
    NewFileEvent(fileName, extension, filePath, event string) error
    UnobservedFiles() []string
    SupportedExtensions() []string
    Name() string
    Label() string
    Value() string
    Change(v string) error
    RefreshUI()
    MainInputFileRelativePath() string
    RegisterRoutes(fn func(*http.ServeMux))
}
```

---

## Middleware Pipeline

The WASI server supports a middleware pipeline for intercepting requests to modules. Middlewares are WASM modules that reside in `modulesDir` and contain a `rule.txt` file at their root.

### `rule.txt` format
- `*` or empty: matches all routes.
- `users,auth`: matches only `users` and `auth` routes.
- `-auth`: matches all routes EXCEPT `auth`.

### Routing behavior (`/m/{name}`)
When a request is made to `/m/{name}`:
1. The server identifies all matching middlewares based on their `rule.txt`.
2. Middlewares are executed in registration order.
3. If a middleware's `handle` export returns a non-zero pointer, execution stops and that pointer's content is returned as the response.
4. If all middlewares return 0, the target module `{name}` is executed.
5. The response is read from the module's memory as a null-terminated string.

### Request Serialization
Requests are passed to the `handle(ptr, len)` export as a simple string: `METHOD\nPATH\n`.

---

## Modified Files Summary

| File | Change |
|---|---|
| `wasi/wasi.go` | `WasiServer` + `New()` + Set* methods + `RegisterRoutes` + ServerInterface |
| `wasi/module.go` | `Module` lifecycle via wazero |
| `wasi/host.go` | `HostBuilder` host functions |
| `wasi/ws_hub.go` | WebSocket relay hub |
| `wasi/go.mod` | wazero, bus, binary, fsnotify |

## Verification

```bash
cd tinywasm/wasi && gotest

# Minimal integration test:
srv := wasi.New().SetPort("6061")
srv.RegisterRoutes(testHandler.RegisterRoutes)
// start + verify routes + verify module load
```
