# Architecture — tinywasm/wasi

## Package Dependencies

```mermaid
graph TD
    subgraph app["tinywasm/app (cmd/tinywasm/main.go)"]
        FAC["ServerFactory\n──────────\nenv: TINYWASM_SERVER=wasi"]
    end

    subgraph wasi["tinywasm/wasi"]
        SRV["WasiServer\n──────────\nStartServer · StopServer\nRestartServer · NewFileEvent"]
        MW["Middleware\n──────────\nRule · Matches · applyPipeline"]
        M["Module\n──────────\nLoad · Drain · Init · Close"]
        HB["HostBuilder\n──────────\nBuild(rt wazero.Runtime)"]
        HUB["wsHub\n──────────\nBroadcast · /ws endpoint"]
    end

    subgraph bus["tinywasm/bus"]
        B["Bus\n──────────\nPublish · Subscribe"]
    end

    subgraph binary["tinywasm/binary"]
        BIN["Message\n──────────\nEncode · Decode"]
    end

    WZ["wazero\n(external)"]

    FAC -->|"wasi.New(cfg)"| SRV
    SRV -->|"Load / swapModule"| M
    SRV -->|"NewHostBuilder"| HB
    SRV -->|"wsHub.Broadcast"| HUB
    SRV -->|"uses"| B
    SRV -->|"applyPipeline"| MW
    MW -->|"invokes"| M
    M   -->|"uses runtime"| WZ
    HB  -->|"registers fns into"| WZ
    HB  -->|"injects"| B
    HB  -->|"injects broadcast fn"| HUB
    M   -->|"payload format"| BIN
```

## Runtime Message Flow

```mermaid
sequenceDiagram
    participant DW  as devwatch
    participant APP as tinywasm/app
    participant SRV as WasiServer<br/>(tinywasm/wasi)
    participant RT  as wazero runtime<br/>(users.wasm)
    participant BUS as tinywasm/bus
    participant HUB as wsHub
    participant BR  as Browser

    Note over DW,APP: Hot-reload trigger
    DW->>APP: modules/users/wasm/*.go changed
    APP->>SRV: NewFileEvent("users.wasm", ".wasm", path, "write")
    SRV->>SRV: swapModule("users", wasmBytes)
    SRV->>RT: Load — compile + instantiate
    SRV->>RT: Drain() — poll until 0 or timeout
    SRV->>BUS: Unsubscribe old subscriptions
    SRV->>RT: old.Close()
    SRV->>RT: new.Init()
    SRV->>BUS: re-register new subscriptions

    Note over RT,BR: Runtime pub/sub flow
    RT->>BUS: host: publish(topic, payload)
    BUS->>RT: host: on_message(payload) → subscriber modules
    RT->>HUB: host: ws_broadcast(topic, payload)
    HUB->>BR: WebSocket frame → subscribed clients

    Note over RT,SRV: HTTP Dispatch (/m/{name})
    BR->>SRV: GET /m/users
    SRV->>MW: applyPipeline("users")
    MW->>RT: middleware.handle()
    SRV->>RT: users.handle()
    RT->>SRV: Response pointer
    SRV->>BR: HTTP Response
```

## ServerInterface Boundary

`WasiServer` fully implements `app.ServerInterface` — it is a **drop-in replacement**
for `tinywasm/server.ServerHandler`. Selection is done in `main.go` via env var:

| `TINYWASM_SERVER` | Concrete server used |
|---|---|
| `wasi` | `tinywasm/wasi.WasiServer` |
| *(unset / any)* | `tinywasm/server.ServerHandler` |

| Concerns | File |
|---|---|
| HTTP server, mux, /m/ dispatch, hot-reload | `wasi.go` |
| Middleware routing (rule.txt) & pipeline | `middleware.go` |
| Module load / drain / init / handle | `module.go` |
| Host function builders (pub, sub, ws_broadcast, log) | `host.go` |
| WebSocket HTTP endpoint (`/ws?topic=`) | `ws_hub.go` |
