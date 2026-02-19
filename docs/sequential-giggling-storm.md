# Plan: tinywasm/wasi Restructuring

## Context

The `tinywasm/wasi` package has three contradictory layers:
- **`docs/WASI_SUPORT.md`** — original prototype concept (simple `handle() string`, no lifecycle)
- **`docs/WASI_SERVER.md`** — design spec using `SetModulesDir`/`SetOutputDir` with default `outputDir = "modules/dist"`
- **`wasi.go`** — actual implementation using `SetSourceDir`/`SetWasmDir` with default `wasmDir = "web/modules"`
- **`example/web/server.go`** — wrong directory structure (server inside `web/`), stub WASM module with no real exports

**Goals:**
1. Align API: rename code fields/methods to match `WASI_SERVER.md`
2. Restructure example: two real communicating modules (sender → pub-sub → receiver → WebSocket)
3. Add middleware rules system (`rule.txt` + `/m/{name}` HTTP dispatch) from original concept
4. Preserve original concept as `ORIGINAL_CONCEPT.md`
5. Update all documentation for consistency

---

## Phase 1: API Renaming — `wasi/wasi.go`

### Field renames
| Old field | New field |
|-----------|-----------|
| `sourceDir` | `modulesDir` |
| `wasmDir` | `outputDir` |

Remove field `workDir` (dead code, not in spec).

### Method renames
| Old method | New method |
|------------|------------|
| `SetSourceDir(dir string) *WasiServer` | `SetModulesDir(dir string) *WasiServer` |
| `SetWasmDir(dir string) *WasiServer` | `SetOutputDir(dir string) *WasiServer` |

Remove `SetWorkDir(dir string)`.

### Default change
```go
// Before
wasmDir: "web/modules",
// After
outputDir: "modules/dist",
```

### New struct field (for middleware — see Phase 3)
```go
middlewares []*MiddlewareModule
muMw        sync.RWMutex
```

### Auto-compile at startup
In `StartServer`, before loading `.wasm` files: scan `modulesDir` subdirectories for `wasm/main.go`; if corresponding `outputDir/<name>.wasm` is absent, call `compileModule(name, ...)`. This makes the example self-bootstrapping.

Fix `compileModule` path logic — current code assumes exactly 4-part path (`X/Y/wasm/main.go`). Replace with:
```go
absModuleRoot := filepath.Join(s.appRootDir, s.modulesDir, name)
absWasmSrc   := filepath.Join(absModuleRoot, "wasm", "main.go")
absOutput    := filepath.Join(s.appRootDir, s.outputDir, name+".wasm")
```

---

## Phase 2: Module Handle Support — `wasi/module.go`

Add `handleFn api.Function` to the `Module` struct (populated in `Load()` if module exports `handle`):

```go
type Module struct {
    // existing fields...
    handleFn api.Function  // optional: exported handle(req_ptr, req_len uint32) uint32
}
```

Add method:
```go
// Handle calls the module's handle() export. Returns the result ptr (into WASM memory).
// Returns 0, nil if handleFn is nil.
func (m *Module) Handle(ctx context.Context, reqPtr, reqLen uint32) (uint32, error)
```

---

## Phase 3: Middleware System — new `wasi/middleware.go`

### Types and functions

```go
// Rule describes which HTTP routes a middleware module applies to.
// Loaded from a module's rule.txt at startup.
type Rule struct {
    All    bool
    Only   []string // apply only to these route names
    Except []string // apply to all except these route names
}

// parseRule parses the content of rule.txt.
//   "*" or ""     → Rule{All: true}
//   "users,auth"  → Rule{Only: ["users","auth"]}
//   "-auth"       → Rule{All: true, Except: ["auth"]}
func parseRule(content string) Rule

// MiddlewareModule pairs a Module with its routing Rule.
type MiddlewareModule struct {
    Module *Module
    Rule   Rule
}

// Matches reports whether this middleware applies to a given route name.
func (mw *MiddlewareModule) Matches(route string) bool

// applyPipeline returns middlewares applicable to route, in registration order.
func applyPipeline(route string, middlewares []*MiddlewareModule) []*MiddlewareModule

// loadRuleFromSourceDir reads modulesDir/<name>/rule.txt.
// Returns (Rule{}, false) if absent — module is not a middleware.
func loadRuleFromSourceDir(modulesDir, name string) (Rule, bool)
```

### `/m/{name}` HTTP route (in `wasi.go` StartServer)

```
GET /m/{name}:
  1. applyPipeline(name, s.middlewares)
  2. For each middleware: if handleFn != nil → Handle(ctx, reqPtr, reqLen)
     - result == 0: continue pipeline
     - result != 0: early exit with that response
  3. Dispatch to s.modules[name].Handle(ctx, reqPtr, reqLen)
  4. Write response from WASM memory to HTTP response
```

Request is serialized as: `METHOD\nPATH\n` (minimal, extensible). Response is the bytes at the returned pointer in WASM memory until a null byte or length prefix.

### Module loading separation at startup

When scanning `outputDir/*.wasm`:
- Call `loadRuleFromSourceDir(s.modulesDir, name)`
- If `rule.txt` exists → load as `*MiddlewareModule`, append to `s.middlewares`
- If not → load as `*Module`, insert into `s.modules` map

Same logic applies in `NewFileEvent` for hot-reload of middleware `.wasm` files.

---

## Phase 4: Example Restructure

### Delete
- `example/web/server.go`
- `example/web/modules/demo/` (entire directory)

### New structure
```
example/
├── main.go                          ← root-level server
├── go.mod                           ← module example
└── modules/
    ├── sender/
    │   ├── wasm/main.go             ← TinyGo: init() publishes to "events"
    │   └── go.mod
    ├── receiver/
    │   ├── wasm/main.go             ← TinyGo: init() subscribes; on_message() ws_broadcasts
    │   └── go.mod
    ├── logger/
    │   ├── rule.txt                 ← content: "*"
    │   ├── wasm/main.go             ← TinyGo: handle() calls log host fn
    │   └── go.mod
    └── dist/
        └── .gitkeep
```

### `example/main.go`
```go
srv := wasi.New().
    SetPort("8080").
    SetAppRootDir(".").
    SetModulesDir("modules").
    SetOutputDir("modules/dist").
    SetDrainTimeout(5 * time.Second)

srv.StartServer(&wg)
```

### TinyGo module pattern (shared across all 3 modules)

Host function imports:
```go
//go:wasmimport env publish
func hostPublish(topicPtr, topicLen, payloadPtr, payloadLen uint32)

//go:wasmimport env subscribe
func hostSubscribe(topicPtr, topicLen, handlerFnIdx uint32)

//go:wasmimport env ws_broadcast
func hostWsBroadcast(topicPtr, topicLen, payloadPtr, payloadLen uint32)

//go:wasmimport env log
func hostLog(msgPtr, msgLen uint32)
```

Required exports:
- `//export init` → called once after module load
- `//export drain` → returns `uint32` (sleep ms, 0 = done)
- `//export on_message` (receiver only) → `func(ptr, len uint32)`
- `//export handle` (logger only) → `func(reqPtr, reqLen uint32) uint32`
- `//export malloc` → `func(size uint32) *byte` — required for pub-sub callbacks

No stdlib imports. Memory helpers use `unsafe.Pointer`.

---

## Phase 5: Tests

### `wasi/wasi_test.go`
- Replace all `SetWasmDir(...)` → `SetOutputDir(...)` (4 occurrences)
- No other changes

### `wasi/wasi_compilation_test.go` — replace entirely
New tests:
- `TestAutoCompile_TriggerOnMissingWasm` — verifies `StartServer` compiles when `.wasm` absent
- `TestNewFileEvent_SwapsModule` — updated paths for new structure
- `TestNewFileEvent_SwapsMiddleware` — hot-reload a middleware module

### New file: `wasi/middleware_test.go`
Pure unit tests (no WASM loading):
- `TestParseRule` — table test: `"*"`, `""`, `"users,auth"`, `"-auth"`, `"users,-admin"`
- `TestMiddlewareModule_Matches` — validates `Matches()` for each Rule combination
- `TestApplyPipeline` — verifies correct filtering by route name

### `wasi_integration_test.go` (new) — pub-sub inter-module test
Uses inline WAT (WebAssembly Text format) — avoids TinyGo dependency in tests:
```wat
;; publisher.wat — calls publish("events","hello") on init
(module
  (import "env" "publish" (func $pub (param i32 i32 i32 i32)))
  (memory (export "memory") 1)
  (data (i32.const 0) "events")
  (data (i32.const 16) "hello")
  (func (export "init")
    i32.const 0  i32.const 6
    i32.const 16 i32.const 5
    call $pub)
  (func (export "drain") (result i32) i32.const 0)
)
```
Test verifies: bus receives published message after module `Init()`.

---

## Phase 6: Documentation

### `docs/WASI_SUPORT.md` → `docs/ORIGINAL_CONCEPT.md`
Git rename only (`git mv`). No content changes.

### `docs/WASI_SERVER.md`
- Update struct definition: `modulesDir`, `outputDir`, no `workDir`
- Update Set* methods table: add `SetModulesDir`, `SetOutputDir`; remove `SetSourceDir`, `SetWasmDir`, `SetWorkDir`
- Update defaults table: `OutputDir → "modules/dist"`
- Add section **Middleware Pipeline** documenting: `rule.txt` format, `MiddlewareModule`, `/m/{name}` route behavior

### `docs/ARCHITECTURE.md`
- Add `middleware.go` to package structure
- Add middleware flow to message flow diagram

### `README.md`
- Update example link to `example/main.go`
- Add `ORIGINAL_CONCEPT.md` entry to docs index
- Update docs table to reflect renamed/new files

---

## Critical Files

| File | Action |
|------|--------|
| `wasi/wasi.go` | Rename fields/methods, auto-compile, `/m/` route, middleware integration |
| `wasi/module.go` | Add `handleFn`, `Handle()` method |
| `wasi/middleware.go` | **CREATE**: `Rule`, `MiddlewareModule`, `parseRule`, `applyPipeline` |
| `wasi/middleware_test.go` | **CREATE**: unit tests |
| `wasi/wasi_integration_test.go` | **CREATE**: WAT-based pub-sub test |
| `wasi/wasi_test.go` | Update `SetWasmDir` → `SetOutputDir` (4x) |
| `wasi/wasi_compilation_test.go` | **REPLACE**: new auto-compile + hot-reload tests |
| `example/main.go` | **CREATE**: root-level server |
| `example/modules/sender/wasm/main.go` | **CREATE**: TinyGo publisher |
| `example/modules/receiver/wasm/main.go` | **CREATE**: TinyGo subscriber+broadcaster |
| `example/modules/logger/wasm/main.go` | **CREATE**: TinyGo middleware |
| `example/modules/logger/rule.txt` | **CREATE**: `*` |
| `example/web/` | **DELETE** (entire directory) |
| `docs/WASI_SUPORT.md` → `docs/ORIGINAL_CONCEPT.md` | **RENAME** |
| `docs/WASI_SERVER.md` | Update API names + middleware section |
| `docs/ARCHITECTURE.md` | Add middleware pipeline |
| `README.md` | Update links and docs index |

---

## Verification

```bash
# 1. Unit tests (pure Go, no TinyGo required)
cd /home/cesar/Dev/Pkg/tinywasm/wasi && gotest

# 2. Run example (requires TinyGo for auto-compile)
cd /home/cesar/Dev/Pkg/tinywasm/wasi/example && go run main.go
# Expected:
#   [WASI] sender: init called
#   [WASI] receiver: subscribed to events
#   [WASI] logger middleware: ready
# Then open: ws://localhost:8080/ws?topic=events  → receives "hello from sender"
# Then GET http://localhost:8080/m/sender         → logger middleware fires
```

## Out of Scope

- `externalWatcher` race condition (separate fix)
- `nhooyr.io/websocket` unused dependency cleanup (separate PR)
- Subscription re-registration on hot-reload (tracked in ARCHITECTURE.md as known gap)
