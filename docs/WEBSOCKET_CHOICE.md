# WebSocket Library Selection: github.com/coder/websocket

## Context

This package (`tinywasm/wasi`) implements the **Host (Server)** logic. It runs on standard Go environments (Linux, macOS, Windows) and acts as the runtime for WebAssembly modules. It is **not** code that runs inside the browser or inside a WASM sandbox.

## Problem

The Go standard library (`net/http`) does not include a built-in WebSocket implementation. While there is a semi-official `golang.org/x/net/websocket`, it is widely considered feature-incomplete and not recommended for new projects.

## Solution: github.com/coder/websocket

We have chosen `github.com/coder/websocket` for the following reasons:

1. **Active Maintenance:** It is the officially recommended, actively maintained fork of the popular `nhooyr/websocket` library.
2. **Minimalist & Idiomatic:** It follows Go best practices, featuring a clean API that integrates seamlessly with `context.Context` and `net/http`.
3. **Standards Compliant:** It strictly follows RFC 6455.
4. **Performance:** It offers excellent performance with zero-allocation reads and writes.
5. **No Dependencies:** It is a lean library with no external dependencies, keeping our footprint small.

## Comparison with Gorilla WebSocket

While `gorilla/websocket` is a common alternative, `coder/websocket` provides a more modern API that is easier to use correctly with Go's concurrency primitives (Context).
