package main

import (
	"log"
	"sync"
	"time"

	"github.com/tinywasm/wasi"
)

func main() {
	var wg sync.WaitGroup

	srv := wasi.New().
		SetPort("8080").
		SetAppRootDir(".").
		SetModulesDir("modules").
		SetOutputDir("modules/dist").
		SetDrainTimeout(5 * time.Second).
		SetLogger(log.Println)

	log.Println("Starting WASI server on :8080...")
	srv.StartServer(&wg)

	wg.Wait()
}
