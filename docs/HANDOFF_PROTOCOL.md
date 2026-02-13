# Graceful Handoff Protocol

## Sequence Diagram

```
devwatch detects modules/users/wasm/*.go changed
    → TinyGo recompiles → users.wasm (new bytes)
    → WasiServer.NewFileEvent("users.wasm", ".wasm", path, "write")
        → WasiServer.swapModule("users", newWasmBytes)
            → hb := NewHostBuilder(s.bus, s.wsHub.Broadcast)
            → newMod, err := Load(ctx, "users", newWasmBytes, hb)
                if err → log, keep old module, return               ← no downtime
            → oldMod := s.modules["users"]
            → oldMod.Drain(ctx, s.cfg.DrainTimeout):
                loop:
                    ms := oldMod.drainFn.Call()
                    if ms == 0: break
                    if elapsed > drainTimeout: log.Warn("drain timeout, forcing swap"); break
                    sleep(time.Duration(ms) * time.Millisecond)
            → s.bus.Unsubscribe(all subscriptions for "users")
            → oldMod.Close(ctx)
            → newMod.Init(ctx)
            → re-register newMod subscriptions
            → s.mu.Lock(); s.modules["users"] = newMod; s.mu.Unlock()
```

## Drain Timeout Config

```go
// In wasi.Config:
DrainTimeout time.Duration // default: 5s if zero
```

Resolved inside `WasiServer.New()`: `if cfg.DrainTimeout == 0 { cfg.DrainTimeout = 5 * time.Second }`.

## Error Cases

- **Module never returns 0**: force swap after `DrainTimeout`, emit warning log
- **New module fails `Load()`**: keep old module running, log error — no downtime
- **New module fails `Init()`**: keep old module running, log error — no downtime
- **wazero compilation error**: keep old module, surface error in TUI via `cfg.Logger`
- **Module panics after load**: recover via wazero context, mark module as failed, keep old
