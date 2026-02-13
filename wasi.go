package wasi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/tinywasm/bus"
)

type WasiServer struct {
	appRootDir   string
	modulesDir   string
	outputDir    string
	port         string
	drainTimeout time.Duration
	routes       []func(*http.ServeMux)
	bus          bus.Bus
	exitChan     chan bool
	logger       func(...any)
	ui           interface{ RefreshUI() }

	mux     *http.ServeMux
	httpSrv *http.Server
	modules map[string]*Module
	mu      sync.RWMutex
	wsHub   *wsHub
	watcher *fsnotify.Watcher
}

// New creates a WasiServer with all defaults. Configure via Set* methods.
func New() *WasiServer {
	return &WasiServer{
		appRootDir:   ".",
		modulesDir:   "modules",
		outputDir:    "modules/dist",
		port:         "6060",
		drainTimeout: 5 * time.Second,
		exitChan:     make(chan bool),
		logger:       func(msg ...any) {},
		ui:           noopUI{},
		bus:          bus.New(),
		modules:      make(map[string]*Module),
	}
}

type noopUI struct{}

func (n noopUI) RefreshUI() {}

func (s *WasiServer) SetAppRootDir(dir string) *WasiServer {
	s.appRootDir = dir
	return s
}

func (s *WasiServer) SetModulesDir(dir string) *WasiServer {
	s.modulesDir = dir
	return s
}

func (s *WasiServer) SetOutputDir(dir string) *WasiServer {
	s.outputDir = dir
	return s
}

func (s *WasiServer) SetPort(port string) *WasiServer {
	s.port = port
	return s
}

func (s *WasiServer) SetDrainTimeout(d time.Duration) *WasiServer {
	s.drainTimeout = d
	return s
}

func (s *WasiServer) SetLogger(fn func(msg ...any)) *WasiServer {
	s.logger = fn
	return s
}

func (s *WasiServer) SetExitChan(ch chan bool) *WasiServer {
	s.exitChan = ch
	return s
}

func (s *WasiServer) SetUI(ui interface{ RefreshUI() }) *WasiServer {
	s.ui = ui
	return s
}

func (s *WasiServer) SetBus(b bus.Bus) *WasiServer {
	s.bus = b
	return s
}

// RegisterRoutes appends fn to the internal route list.
// Called before StartServer; matching the assetmin pattern.
func (s *WasiServer) RegisterRoutes(fn func(*http.ServeMux)) *WasiServer {
	s.routes = append(s.routes, fn)
	return s
}

// ServerInterface Implementation

// StartServer starts the server.
func (s *WasiServer) StartServer(wg *sync.WaitGroup) {
	// 1. Build mux: register s.routes + wsHub.RegisterRoute
	s.mux = http.NewServeMux()
	for _, route := range s.routes {
		route(s.mux)
	}

	// Initialize wsHub if not present
    if s.wsHub == nil {
        s.wsHub = &wsHub{
            clients: make(map[string]map[*wsConn]bool),
            bus: s.bus,
        }
    }
	s.wsHub.RegisterRoute(s.mux)

	// 2. Load all *.wasm from outputDir → swapModule(name, bytes)
    files, err := filepath.Glob(filepath.Join(s.outputDir, "*.wasm"))
    if err == nil {
        for _, file := range files {
             bytes, err := os.ReadFile(file)
             if err == nil {
                 name := filepath.Base(file)
                 name = name[:len(name)-len(filepath.Ext(name))]
                 s.swapModule(name, bytes)
             }
        }
    }

	// 3. Start fsnotify watcher on outputDir
	watcher, err := fsnotify.NewWatcher()
	if err == nil {
		s.watcher = watcher
		if err := s.watcher.Add(s.outputDir); err != nil {
			s.logger("Watcher add failed:", err)
		} else {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case event, ok := <-watcher.Events:
						if !ok {
							return
						}
						if event.Has(fsnotify.Write) {
							ext := filepath.Ext(event.Name)
							if ext == ".wasm" {
								name := filepath.Base(event.Name)
								s.NewFileEvent(name, ext, event.Name, "write")
							}
						}
					case err, ok := <-watcher.Errors:
						if !ok {
							return
						}
						s.logger("Watcher error:", err)
					}
				}
			}()
		}
	} else {
		s.logger("Watcher failed to start:", err)
	}

	// 4. http.ListenAndServe(port, mux) in goroutine
    s.httpSrv = &http.Server{
        Addr:    ":" + s.port,
        Handler: s.mux,
    }

	wg.Add(1)
	go func() {
		defer wg.Done()

        go func() {
            if err := s.httpSrv.ListenAndServe(); err != http.ErrServerClosed {
                s.logger("HTTP server error: ", err)
            }
        }()

        <-s.exitChan
        s.StopServer()
	}()
}

func (s *WasiServer) StopServer() error {
    // 1. Stop watcher
    if s.watcher != nil {
        s.watcher.Close()
    }

    // 2. For each module: Drain(ctx, drainTimeout) → Close(ctx)
    ctx := context.Background()

    s.mu.RLock()
    mods := make([]*Module, 0, len(s.modules))
    for _, mod := range s.modules {
        mods = append(mods, mod)
    }
    s.mu.RUnlock()

    for _, mod := range mods {
        mod.Drain(ctx, s.drainTimeout)
        mod.Close(ctx)
    }

    // 3. httpSrv.Shutdown(ctx)
    if s.httpSrv != nil {
        return s.httpSrv.Shutdown(ctx)
    }
	return nil
}

func (s *WasiServer) RestartServer() error {
    // Hot-reload all modules.
    files, err := filepath.Glob(filepath.Join(s.outputDir, "*.wasm"))
    if err != nil {
        return err
    }
    for _, file := range files {
         bytes, err := os.ReadFile(file)
         if err == nil {
             name := filepath.Base(file)
             name = name[:len(name)-len(filepath.Ext(name))]
             s.swapModule(name, bytes)
         }
    }
	return nil
}

func (s *WasiServer) NewFileEvent(fileName, extension, filePath, event string) error {
	if event == "write" && extension == ".wasm" {
		name := fileName[:len(fileName)-len(extension)]
		bytes, err := os.ReadFile(filePath)
        if err != nil {
            return err
        }
		return s.swapModule(name, bytes)
	}
	return nil
}

func (s *WasiServer) UnobservedFiles() []string {
	return []string{filepath.Join(s.outputDir, "*.wasm")}
}

func (s *WasiServer) SupportedExtensions() []string {
	return []string{".wasm", ".go"}
}

func (s *WasiServer) Name() string { return "WASI Server" }
func (s *WasiServer) Label() string { return "Server Mode" }
func (s *WasiServer) Value() string { return "wasi" }
func (s *WasiServer) Change(string) error { return nil }
func (s *WasiServer) RefreshUI() { s.ui.RefreshUI() }

// swapModule loads a new module, initializes it, then replaces the old one.
func (s *WasiServer) swapModule(name string, wasmBytes []byte) error {
	// 1. Load (outside lock)
	ctx := context.Background()
	// Ensure wsHub is initialized. StartServer initializes it.
    // If we are here via NewFileEvent but StartServer hasn't run, we might panic.
    // But NewFileEvent is usually driven by watcher started in StartServer, or devwatch.
    if s.wsHub == nil {
        // Just in case, initialize it.
        s.mu.Lock()
        if s.wsHub == nil {
             s.wsHub = &wsHub{
                clients: make(map[string]map[*wsConn]bool),
                bus: s.bus,
            }
        }
        s.mu.Unlock()
    }

	hb := NewHostBuilder(s.bus, s.wsHub.Broadcast, s.logger)
	newMod, err := Load(ctx, name, wasmBytes, hb)
	if err != nil {
		s.logger("Load module error:", err)
		return err
	}

	// 2. Init (outside lock)
	if err := newMod.Init(ctx); err != nil {
		s.logger("Init module error:", err)
		newMod.Close(ctx)
		return err
	}

	// 3. Swap (inside lock)
	s.mu.Lock()
	oldMod := s.modules[name]
	s.modules[name] = newMod
	s.mu.Unlock()

	// 4. Drain Old (outside lock)
	if oldMod != nil {
		oldMod.Drain(ctx, s.drainTimeout)
		oldMod.Close(ctx)
	}

	return nil
}
