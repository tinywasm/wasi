package main

import "unsafe"

//go:wasmimport env subscribe
func hostSubscribe(topicPtr, topicLen, handlerFnIdx uint32)

//go:wasmimport env ws_broadcast
func hostWsBroadcast(topicPtr, topicLen, payloadPtr, payloadLen uint32)

//go:wasmimport env log
func hostLog(msgPtr, msgLen uint32)

//export init
func init_module() {
	msg := "receiver: init called"
	hostLog(uint32(uintptr(unsafe.Pointer(unsafe.StringData(msg)))), uint32(len(msg)))

	topic := "events"
	hostLog(uint32(uintptr(unsafe.Pointer(unsafe.StringData("receiver: subscribing to events")))), 29)
	hostSubscribe(uint32(uintptr(unsafe.Pointer(unsafe.StringData(topic)))), uint32(len(topic)), 0)
}

//export on_message
func on_message(ptr, msgLen uint32) {
	msg := "receiver: received message"
	hostLog(uint32(uintptr(unsafe.Pointer(unsafe.StringData(msg)))), uint32(len(msg)))

	topic := "events"
	hostWsBroadcast(
		uint32(uintptr(unsafe.Pointer(unsafe.StringData(topic)))), uint32(len(topic)),
		ptr, msgLen,
	)
}

//export drain
func drain() uint32 {
	return 0
}

//export malloc
func malloc(size uint32) uintptr {
	buf := make([]byte, size)
	return uintptr(unsafe.Pointer(&buf[0]))
}

func main() {}
