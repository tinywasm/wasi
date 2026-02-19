package wasi

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type Module struct {
	name     string
	runtime  wazero.Runtime
	mod      api.Module
	active   atomic.Int32
	drainFn  api.Function // exported drain() uint32
	initFn   api.Function // exported init()
	handleFn api.Function // optional: exported handle(req_ptr, req_len uint32) uint32
	cleanups []func()
}

type moduleKey struct{}

func Load(ctx context.Context, name string, wasmBytes []byte, hb *HostBuilder) (*Module, error) {
	r := wazero.NewRuntime(ctx)

	// Enable WASI
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		r.Close(ctx)
		return nil, err
	}

	// Build host module
	if _, err := hb.Build(r).Instantiate(ctx); err != nil {
		r.Close(ctx)
		return nil, err
	}

	// Compile module (handles WAT or WASM)
	compiled, err := r.CompileModule(ctx, wasmBytes)
	if err != nil {
		r.Close(ctx)
		return nil, err
	}

	m := &Module{
		name:    name,
		runtime: r,
	}

	// Pass m in context so host functions can access it
	ctxWithModule := context.WithValue(ctx, moduleKey{}, m)

	mod, err := r.InstantiateModule(ctxWithModule, compiled, wazero.NewModuleConfig())
	if err != nil {
		r.Close(ctx)
		return nil, err
	}
	m.mod = mod

	m.drainFn = mod.ExportedFunction("drain")
	m.initFn = mod.ExportedFunction("init")
	m.handleFn = mod.ExportedFunction("handle")

	return m, nil
}

func (m *Module) Drain(ctx context.Context, timeout time.Duration) error {
	if m.drainFn == nil {
		return nil
	}

	start := time.Now()
	for {
		results, err := m.drainFn.Call(ctx)
		if err != nil {
			// If error, maybe we should stop draining?
			return err
		}
		// Assuming drain returns uint32 (ms)
		if len(results) == 0 {
			break
		}
		ms := uint32(results[0])
		if ms == 0 {
			break
		}

		if time.Since(start) > timeout {
			// Timeout
			break
		}

		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	return nil
}

func (m *Module) Init(ctx context.Context) error {
	if m.initFn != nil {
		_, err := m.initFn.Call(ctx)
		return err
	}
	return nil
}

func (m *Module) Close(ctx context.Context) error {
	// Unsubscribe
	for _, cleanup := range m.cleanups {
		cleanup()
	}
	return m.runtime.Close(ctx)
}

// Handle calls the module's handle() export. Returns the result ptr (into WASM memory).
// Returns 0, nil if handleFn is nil.
func (m *Module) Handle(ctx context.Context, reqPtr, reqLen uint32) (uint32, error) {
	if m.handleFn == nil {
		return 0, nil
	}
	results, err := m.handleFn.Call(ctx, uint64(reqPtr), uint64(reqLen))
	if err != nil {
		return 0, err
	}
	if len(results) == 0 {
		return 0, nil
	}
	return uint32(results[0]), nil
}
