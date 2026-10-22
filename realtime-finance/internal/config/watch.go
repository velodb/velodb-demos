package config

import (
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher hot-reloads strategy.toml, market.toml and stocks/*.toml.
// Editors save in many different ways (write in place, write temp + rename,
// truncate + write), so we watch the parent directories and debounce per path.
type Watcher struct {
	Dir      string // project directory holding strategy.toml and market.toml
	StockDir string

	OnStrategy func(*Strategy)
	OnMarket   func(*Market)
	OnStock    func(*Stock)        // a stock file was added or changed
	OnDelete   func(symbol string) // a stock file was removed
	OnError    func(path string, err error)
	OnOK       func(path string)

	mu      sync.Mutex
	pending map[string]*time.Timer
}

func (w *Watcher) Start() error {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := fw.Add(w.Dir); err != nil {
		return err
	}
	if err := fw.Add(w.StockDir); err != nil {
		return err
	}
	w.pending = map[string]*time.Timer{}
	go func() {
		for {
			select {
			case ev, ok := <-fw.Events:
				if !ok {
					return
				}
				w.schedule(ev)
			case err, ok := <-fw.Errors:
				if !ok {
					return
				}
				log.Printf("config watcher: %v", err)
			}
		}
	}()
	return nil
}

func (w *Watcher) schedule(ev fsnotify.Event) {
	name := filepath.Base(ev.Name)
	if !strings.HasSuffix(name, ".toml") || strings.HasPrefix(name, ".") {
		return
	}
	dir := filepath.Dir(ev.Name)
	isStock := filepath.Clean(dir) == filepath.Clean(w.StockDir)
	isGlobal := filepath.Clean(dir) == filepath.Clean(w.Dir) && (name == "strategy.toml" || name == "market.toml")
	if !isStock && !isGlobal {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if t, ok := w.pending[ev.Name]; ok {
		t.Stop()
	}
	path := ev.Name
	w.pending[path] = time.AfterFunc(250*time.Millisecond, func() {
		w.mu.Lock()
		delete(w.pending, path)
		w.mu.Unlock()
		w.reload(path, isStock)
	})
}

func (w *Watcher) reload(path string, isStock bool) {
	name := filepath.Base(path)
	if isStock {
		symbol := strings.TrimSuffix(name, ".toml")
		if !fileExists(path) {
			if w.OnDelete != nil {
				w.OnDelete(symbol)
			}
			return
		}
		s, err := LoadStock(path)
		if err != nil {
			w.fail(path, err)
			return
		}
		if w.OnStock != nil {
			w.OnStock(s)
		}
		w.ok(path)
		return
	}
	switch name {
	case "strategy.toml":
		s, err := LoadStrategy(path)
		if err != nil {
			w.fail(path, err)
			return
		}
		if w.OnStrategy != nil {
			w.OnStrategy(s)
		}
	case "market.toml":
		m, err := LoadMarket(path)
		if err != nil {
			w.fail(path, err)
			return
		}
		if w.OnMarket != nil {
			w.OnMarket(m)
		}
	}
	w.ok(path)
}

func (w *Watcher) fail(path string, err error) {
	log.Printf("config reload rejected: %v", err)
	if w.OnError != nil {
		w.OnError(path, err)
	}
}

func (w *Watcher) ok(path string) {
	log.Printf("config reloaded: %s", filepath.Base(path))
	if w.OnOK != nil {
		w.OnOK(path)
	}
}
