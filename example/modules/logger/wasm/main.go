package main

import "unsafe"

//go:wasmimport env log
func hostLog(msgPtr, msgLen uint32)

//export init
func init_module() {
	msg := "logger middleware: ready"
	hostLog(uint32(uintptr(unsafe.Pointer(unsafe.StringData(msg)))), uint32(len(msg)))
}

//export handle
func handle(reqPtr, reqLen uint32) uint32 {
	msg := "logger middleware: intercepting request"
	hostLog(uint32(uintptr(unsafe.Pointer(unsafe.StringData(msg)))), uint32(len(msg)))
	return 0 // continue pipeline
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
