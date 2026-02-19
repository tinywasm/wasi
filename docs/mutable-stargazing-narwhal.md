# WASI Dynamic Module System — Central Plan

> **Status**: In Progress. Base 2 and Base 3 completed.
> **Language**: English (per protocol)
> **Strategic Justification**: This architecture provides a high-performance, safe, and hot-reloadable module system for Go applications using WebAssembly. By using WASI and wazero, we achieve platform independence and security isolation without sacrificing developer experience.

## Overview

This plan adds a **dynamic WASI module system** to the tinywasm stack. Modules in the user's application project can expose a `wasm/` subdirectory that gets compiled to a `.wasm` binary and loaded at runtime by the server with hot-reload and pub/sub communication.

**Build tag convention**: `//go:build !wasm` = server-side (standard Go). `//go:build wasm` = browser (TinyGo WASM). WASI modules (`modules/*/wasm/`) are backend code compiled by TinyGo; they use `!wasm` libraries via the build pipeline (Base 4) and interact with `bus` through host functions, never via direct import.

**Message Envelope**: Defined in `tinywasm/binary`, uses `tinywasm/fmt.MessageType` for unified classification across the system.

**Bus**: single implementation (no build tag split) — slices + `sync.RWMutex`, compatible with both standard Go and TinyGo.

## Plan Bases (Execution Details)

The plan is divided into 5 modular bases for specialized review:

1.  **[Base 1: tinywasm/wasi — WASI Runtime Core](../../Dev/Pkg/tinywasm/wasi/docs/WASI_STRATEGY.md)**
    *   `Module` lifecycle (load, drain, init, close) via wazero — lives in `tinywasm/wasi`.
    *   `HostBuilder` — host functions for Pub/Sub and WebSocket relay — lives in `tinywasm/wasi`.
    *   `wasiStrategy` adapter + `wsHub` + `Config` changes — stay in `tinywasm/server`.
    *   **[Handoff Protocol](../../Dev/Pkg/tinywasm/wasi/docs/HANDOFF_PROTOCOL.md)** — hot-swap drain sequence (Base 5 merged here).
2.  ✅ **[Base 2: tinywasm/bus — Bus Architecture](../../Dev/Pkg/tinywasm/bus/docs/bus-architecture.md)**
    *   Dual-implementation pub/sub hub (backend + browser frontend).
    *   Single implementation using slices + `sync.RWMutex` for TinyGo compatibility.
    *   WASI modules interact via `hostPublish`/`hostSubscribe` host functions — they do NOT import `bus`.
3.  ✅ **[Base 3: tinywasm/binary — Message Envelope](../../Dev/Pkg/tinywasm/binary/docs/message-envelope.md)**
    *   Defines the wire format for module communication.
    *   Uses `fmt.MessageType` (Event, Request, Response, Error) for routing logic.
4.  **[Base 4: tinywasm/app — Build Pipeline Integration](../../Dev/Pkg/tinywasm/app/docs/build-integration.md)**
    *   Updates `section-build.go` to support TinyGo WASI compilation.
5.  ✅ **Base 5: Graceful Handoff Protocol** — merged into Base 1 ([HANDOFF_PROTOCOL.md](../../Dev/Pkg/tinywasm/wasi/docs/HANDOFF_PROTOCOL.md))

## Verification Plan

1.  **Unit**: `gotest` in `tinywasm/bus` — subscribe/publish/cancel
2.  **Unit**: `gotest` in `tinywasm/binary` — encode/decode `Message` type
3.  **Integration**: Start server with wasiStrategy → load `modules/users/wasm/` → verify HTTP + WS response
4.  **Hot-reload**: Modify `modules/users/wasm/main.go` → verify swap without HTTP downtime
5.  **Drain**: Add artificial delay in `drain()` → verify server waits before swapping
6.  **Pub/Sub**: Module A publishes → Module B (subscriber) receives via `on_message()`

## Implementation Order

1.  `git checkout -b feature/wasi-modules` in `tinywasm/server`
2.  Add `binary.Message` type to `tinywasm/binary`
3.  Create `tinywasm/bus` package
4.  Extend `tinywasm/server` with WASI support and host functions
5.  Update `tinywasm/app` build pipeline
6.  Final verification and documentation update
