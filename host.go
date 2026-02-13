package wasi

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tinywasm/binary"
	"github.com/tinywasm/bus"
)

type HostBuilder struct {
	bus         bus.Bus
	wsBroadcast func(topic string, msg []byte)
	logger      func(msg ...any)
}

func NewHostBuilder(b bus.Bus, wsBroadcast func(topic string, msg []byte), logger func(msg ...any)) *HostBuilder {
	return &HostBuilder{
		bus:         b,
		wsBroadcast: wsBroadcast,
		logger:      logger,
	}
}

func (h *HostBuilder) Build(rt wazero.Runtime) wazero.HostModuleBuilder {
	return rt.NewHostModuleBuilder("env").
		NewFunctionBuilder().WithFunc(h.publish).Export("publish").
		NewFunctionBuilder().WithFunc(h.subscribe).Export("subscribe").
		NewFunctionBuilder().WithFunc(h.wsBroadcastFunc).Export("ws_broadcast").
		NewFunctionBuilder().WithFunc(h.log).Export("log")
}

func (h *HostBuilder) publish(ctx context.Context, m api.Module, topicPtr, topicLen, payloadPtr, payloadLen uint32) {
	topic := readString(m, topicPtr, topicLen)
	payload := readBytes(m, payloadPtr, payloadLen)
	h.bus.Publish(topic, binary.Message{Payload: payload})
}

func (h *HostBuilder) subscribe(ctx context.Context, m api.Module, topicPtr, topicLen, handlerFnIdx uint32) {
	topic := readString(m, topicPtr, topicLen)

	// Retrieve the Module struct to register cleanup
	modVal := ctx.Value(moduleKey{})
	if modVal == nil {
		h.logString(ctx, m, "Error: Module not found in context for subscribe")
		return
	}
	modInstance := modVal.(*Module)

	// We need to call on_message(ptr, len) in the module.
	onMessage := m.ExportedFunction("on_message")
	if onMessage == nil {
		h.logString(ctx, m, "Error: on_message not exported")
		return
	}

	malloc := m.ExportedFunction("malloc")
	if malloc == nil {
		malloc = m.ExportedFunction("alloc")
	}

	sub := h.bus.Subscribe(topic, func(msg binary.Message) {
		// This callback is running in a goroutine managed by bus.
		// Use background context for callback to avoid using cancelled context from subscribe call.
		bgCtx := context.Background()

		// We need to allocate memory for msg.
		if malloc == nil {
			// Cannot allocate
			return
		}

		// Allocate memory
		results, err := malloc.Call(bgCtx, uint64(len(msg.Payload)))
		if err != nil {
			return
		}
		ptr := uint32(results[0])

		// Write msg to memory
		if !m.Memory().Write(ptr, msg.Payload) {
			return
		}

		// Call on_message
		_, err = onMessage.Call(bgCtx, uint64(ptr), uint64(len(msg.Payload)))
		if err != nil {
			// use logger? But inside callback we might race or need context.
			// Just verify logger usage in main thread calls.
		}
	})

	modInstance.cleanups = append(modInstance.cleanups, func() {
		sub.Cancel()
	})
}

func (h *HostBuilder) wsBroadcastFunc(ctx context.Context, m api.Module, topicPtr, topicLen, payloadPtr, payloadLen uint32) {
	topic := readString(m, topicPtr, topicLen)
	payload := readBytes(m, payloadPtr, payloadLen)
	if h.wsBroadcast != nil {
		h.wsBroadcast(topic, payload)
	}
}

func (h *HostBuilder) log(ctx context.Context, m api.Module, msgPtr, msgLen uint32) {
	msg := readString(m, msgPtr, msgLen)
	h.logString(ctx, m, msg)
}

func (h *HostBuilder) logString(ctx context.Context, m api.Module, msg string) {
	if h.logger != nil {
		h.logger("[WASI]", msg)
	} else {
		fmt.Println("[WASI]", msg)
	}
}

func readString(m api.Module, offset, length uint32) string {
	if length == 0 {
		return ""
	}
	buf, ok := m.Memory().Read(offset, length)
	if !ok {
		return ""
	}
	return string(buf)
}

func readBytes(m api.Module, offset, length uint32) []byte {
	if length == 0 {
		return nil
	}
	buf, ok := m.Memory().Read(offset, length)
	if !ok {
		return nil
	}
	out := make([]byte, length)
	copy(out, buf)
	return out
}
