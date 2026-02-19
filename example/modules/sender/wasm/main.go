package main

import "unsafe"

//go:wasmimport env publish
func hostPublish(topicPtr, topicLen, payloadPtr, payloadLen uint32)

//go:wasmimport env log
func hostLog(msgPtr, msgLen uint32)

//export init
func init_module() {
	msg := "sender: init called"
	hostLog(uint32(uintptr(unsafe.Pointer(unsafe.StringData(msg)))), uint32(len(msg)))

	topic := "events"
	payload := "hello from sender"
	hostPublish(
		uint32(uintptr(unsafe.Pointer(unsafe.StringData(topic)))), uint32(len(topic)),
		uint32(uintptr(unsafe.Pointer(unsafe.StringData(payload)))), uint32(len(payload)),
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
