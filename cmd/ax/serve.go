// ax serve and ax watch: live re-assay on save.
//
// The watcher is a dependency-free poller rather than an inotify/kqueue
// binding: every interval it walks the tree and hashes (path, size, mtime)
// of source files. Polling costs a few milliseconds per second on real
// trees, behaves identically on every platform, and keeps the module's
// dependency set exactly where it is. The digest is order-independent and
// pure, so equal trees always produce equal digests.
package main

import (
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"goforge.dev/assayxport/internal/explorer"
)

// watchInterval is how often the poller re-walks the tree, and settleDelay
// is how long the digest must hold still before a rebuild fires, so a save
// burst (formatter + editor + go generate) coalesces into one re-assay.
const (
	watchInterval = 700 * time.Millisecond
	settleDelay   = 250 * time.Millisecond
)

// watchExts are the file extensions whose changes trigger a re-assay,
// mirroring the extractors' inputs.
var watchExts = map[string]bool{
	".go": true, ".py": true, ".java": true,
}

// watchFiles are basename triggers regardless of extension.
var watchFiles = map[string]bool{
	"go.mod": true, "go.sum": true,
}

// skipDirs are never walked: outputs, VCS metadata, and dependency caches.
var skipDirs = map[string]bool{
	".assayxport": true, ".git": true, ".hg": true, ".svn": true,
	"node_modules": true, "__pycache__": true, ".venv": true, "venv": true,
	"target": true, "vendor": true,
}

// digest hashes the identity of every watched file under root. Two trees
// with the same watched files, sizes, and mtimes produce the same digest.
func digest(root string) uint64 {
	h := fnv.New64a()
	// filepath.WalkDir visits entries in lexical order, so the fold over
	// (path, size, mtime) is deterministic without collecting and sorting.
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries just don't contribute
		}
		name := d.Name()
		if d.IsDir() {
			if skipDirs[name] || (len(name) > 1 && strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if !watchExts[filepath.Ext(name)] && !watchFiles[name] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		fmt.Fprintf(h, "%s|%d|%d\n", path, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	return h.Sum64()
}

// waitForChange blocks until the tree's digest changes from last and then
// holds still for settleDelay, returning the new stable digest.
func waitForChange(root string, last uint64) uint64 {
	for {
		time.Sleep(watchInterval)
		d := digest(root)
		if d == last {
			continue
		}
		// Change seen: wait for the burst to settle.
		for {
			time.Sleep(settleDelay)
			d2 := digest(root)
			if d2 == d {
				return d
			}
			d = d2
		}
	}
}

// hub fans a "reload" signal out to every connected SSE client.
type hub struct {
	mu   sync.Mutex
	subs map[chan struct{}]bool
}

func newHub() *hub { return &hub{subs: make(map[chan struct{}]bool)} }

func (h *hub) subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.subs[ch] = true
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(ch chan struct{}) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

func (h *hub) broadcast() {
	h.mu.Lock()
	for ch := range h.subs {
		select {
		case ch <- struct{}{}:
		default: // a slow client already has a pending signal
		}
	}
	h.mu.Unlock()
}

// current holds the latest snapshot under a lock so every request sees a
// complete, consistent set of artifacts even while a re-assay is building the
// next one.
type current struct {
	mu   sync.RWMutex
	snap *snapshot
}

func (c *current) set(s *snapshot) {
	c.mu.Lock()
	c.snap = s
	c.mu.Unlock()
}

func (c *current) get() *snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snap
}

func runServeCmd(args []string) error {
	fs2 := flag.NewFlagSet("ax serve", flag.ContinueOnError)
	port := fs2.Int("port", 7979, "port to listen on")
	noWatch := fs2.Bool("no-watch", false, "serve a single assay; do not re-assay on save")
	noWasm := fs2.Bool("no-wasm", false, "serve the whole manifest inline instead of the lazy WASM explorer")
	quiet := fs2.Bool("quiet", false, "suppress progress on stderr")
	var langs stringsFlag
	fs2.Var(&langs, "lang", "language to assay (repeatable; default: all)")
	fs2.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: ax serve [path] [flags]")
		fs2.PrintDefaults()
	}
	path, rest := splitPath(args)
	if err := fs2.Parse(rest); err != nil {
		return err
	}
	if fs2.NArg() > 0 {
		path = fs2.Arg(0)
	}
	watch := !*noWatch
	// Lazy (WASM) mode is the default: the page fetches the index up front and
	// each package's shard on demand, so a large tree (thousands of packages)
	// is explorable without ever building one multi-hundred-MB page. --no-wasm
	// falls back to inlining the whole manifest, the same self-contained page
	// `ax assay` writes -- handy for a small tree or an environment without
	// WebAssembly.
	lazy := !*noWasm

	// One server-owned temp root holds each lazy assay's shards on disk. Each
	// assay writes a fresh gen-<N>/ subdir; the previous generation is removed
	// after a grace delay so in-flight reads of it stay consistent. Since
	// ListenAndServe blocks (defers never run on SIGINT), a signal handler
	// removes the root too.
	var tmpRoot string
	if lazy {
		d, mkErr := os.MkdirTemp("", "ax-serve-*")
		if mkErr != nil {
			return mkErr
		}
		tmpRoot = d
		defer os.RemoveAll(tmpRoot)
		sigc := make(chan os.Signal, 1)
		signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigc
			os.RemoveAll(tmpRoot)
			os.Exit(0)
		}()
	}
	var gen uint64

	// build runs one assay. In lazy mode it streams the shards to a fresh
	// generation dir and returns a disk-backed snapshot (releasing the in-RAM
	// shard graph); in --no-wasm mode it inlines the whole manifest. The second
	// return is the generation dir (empty in --no-wasm) for delayed cleanup.
	build := func() (*snapshot, string, error) {
		if !lazy {
			idx, shards, err := assayOnce(path, langs, *quiet)
			if err != nil {
				return nil, "", err
			}
			snap, err := buildSnapshot(idx, shards, watch)
			return snap, "", err
		}
		// Lazy mode streams shards straight to a fresh generation dir, so the
		// whole symbol graph is never held in RAM at once.
		g := atomic.AddUint64(&gen, 1)
		dir := filepath.Join(tmpRoot, fmt.Sprintf("gen-%d", g))
		idx, err := assayToDir(path, langs, *quiet, dir)
		if err != nil {
			return nil, "", err
		}
		snap, err := buildDiskSnapshot(idx, dir)
		if err != nil {
			return nil, "", err
		}
		return snap, dir, nil
	}

	cur := &current{}
	h := newHub()

	// The first assay runs in the background so the port binds immediately and
	// the page shows an "analyzing" state; on completion (and on every save in
	// watch mode) the snapshot is swapped in atomically. The previous generation
	// dir is removed after a grace delay so readers mid-request are not cut off.
	go func() {
		last := digest(path)
		prevDir := ""
		first := true
		for {
			if !first {
				last = waitForChange(path, last)
			}
			start := time.Now()
			snap, dir, err := build()
			if err != nil {
				if first {
					fmt.Fprintln(os.Stderr, "ax: assay failed:", err)
				} else {
					// A save mid-edit can be unparseable; keep serving the last
					// good assay and say why.
					fmt.Fprintln(os.Stderr, "ax: re-assay failed (serving last good):", err)
				}
				if !watch {
					return
				}
				first = false
				continue
			}
			cur.set(snap)
			if lazy {
				// The assay's in-RAM struct graph is now unreferenced (the disk
				// snapshot keeps only the index and shard-path set); return that
				// peak to the OS promptly rather than waiting for the background
				// scavenger, so steady-state RSS reflects what is actually served.
				debug.FreeOSMemory()
			}
			if !first {
				h.broadcast()
				if !*quiet {
					fmt.Fprintf(os.Stderr, "ax: re-assayed in %s\n", time.Since(start).Round(time.Millisecond))
				}
			}
			if prevDir != "" {
				d := prevDir
				go func() { time.Sleep(2 * time.Second); os.RemoveAll(d) }()
			}
			prevDir = dir
			first = false
			if !watch {
				return
			}
		}
	}()

	// The lazy page shell is assay-independent, so it is precomputed once and
	// served even before the first assay finishes (the WASM client boots and
	// polls /api/index, which returns 503 until the index is ready).
	shell := explorer.Shell(watch)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" && r.URL.Path != "/assayxport.html" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if lazy {
			_, _ = w.Write(shell)
			return
		}
		s := cur.get()
		if s == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "<!doctype html><title>assayxport</title><p>Analyzing…</p>")
			return
		}
		_, _ = w.Write(s.embedded)
	})
	// Lazy API: the lean index up front, then one package's shard at a time.
	// Served regardless of --no-wasm so /assayxport.json's shape stays available
	// to scripts either way. All three return 503 until the first assay lands.
	mux.HandleFunc("/api/index", func(w http.ResponseWriter, r *http.Request) {
		s := cur.get()
		if s == nil {
			writeAnalyzing(w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(s.indexJSON)
	})
	mux.HandleFunc("/api/shard", func(w http.ResponseWriter, r *http.Request) {
		s := cur.get()
		if s == nil {
			writeAnalyzing(w)
			return
		}
		p := r.URL.Query().Get("path")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if !lazy {
			body, ok := s.shards[p]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(body)
			return
		}
		// Disk mode: only paths the manifest emitted are valid keys (so `../`
		// traversal is never a key), with a cleaned-prefix check as defense in
		// depth before touching the filesystem.
		if !s.shardPaths[p] {
			http.NotFound(w, r)
			return
		}
		full := filepath.Join(s.dir, filepath.FromSlash(p))
		if full != s.dir && !strings.HasPrefix(full, s.dir+string(os.PathSeparator)) {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(full)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		_, _ = io.Copy(w, f)
	})
	if lazy {
		mux.HandleFunc("/static/explorer.wasm", gzAsset(explorer.WasmGz(), "application/wasm"))
		mux.HandleFunc("/static/wasm_exec.js", gzAsset(explorer.WasmExecGz(), "text/javascript; charset=utf-8"))
	}
	mux.HandleFunc("/assayxport.json", func(w http.ResponseWriter, r *http.Request) {
		s := cur.get()
		if s == nil {
			writeAnalyzing(w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if lazy {
			if err := streamCombined(w, s.dir, s.shardPaths); err != nil {
				fmt.Fprintln(os.Stderr, "ax: stream /assayxport.json:", err)
			}
			return
		}
		_, _ = w.Write(s.combined)
	})
	mux.HandleFunc("/__events", func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Connection", "keep-alive")
		fmt.Fprint(w, ": connected\n\n")
		fl.Flush()
		ch := h.subscribe()
		defer h.unsubscribe(ch)
		keepalive := time.NewTicker(25 * time.Second)
		defer keepalive.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ch:
				fmt.Fprint(w, "event: reload\ndata: {}\n\n")
				fl.Flush()
			case <-keepalive.C:
				fmt.Fprint(w, ": keepalive\n\n")
				fl.Flush()
			}
		}
	})

	addr := "127.0.0.1:" + strconv.Itoa(*port)
	if !*quiet {
		mode := "watching for changes"
		if !watch {
			mode = "static (--no-watch)"
		}
		engine := "lazy WASM"
		if !lazy {
			engine = "inline (--no-wasm)"
		}
		fmt.Fprintf(os.Stderr, "ax: exploring %s at http://%s (%s, %s)\n", path, addr, engine, mode)
	}
	return http.ListenAndServe(addr, mux)
}

func runWatchCmd(args []string) error {
	fs2 := flag.NewFlagSet("ax watch", flag.ContinueOnError)
	out := fs2.String("out", "", "output directory (default: assay path)")
	quiet := fs2.Bool("quiet", false, "suppress progress on stderr")
	noHTML := fs2.Bool("no-html", false, "skip writing assayxport.html")
	var langs stringsFlag
	fs2.Var(&langs, "lang", "language to assay (repeatable; default: all)")
	fs2.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: ax watch [path] [flags]")
		fs2.PrintDefaults()
	}
	path, rest := splitPath(args)
	if err := fs2.Parse(rest); err != nil {
		return err
	}
	if fs2.NArg() > 0 {
		path = fs2.Arg(0)
	}
	outDir := *out
	if outDir == "" {
		outDir = path
	}

	once := func() {
		start := time.Now()
		idx, shards, err := assayOnce(path, langs, *quiet)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ax: assay failed (artifacts unchanged):", err)
			return
		}
		if err := writeAll(outDir, idx, shards, *noHTML); err != nil {
			fmt.Fprintln(os.Stderr, "ax: write failed:", err)
			return
		}
		if !*quiet {
			fmt.Fprintf(os.Stderr, "ax: %s  %d packages → %s (%s)\n",
				time.Now().Format("15:04:05"), len(idx.Packages), outDir,
				time.Since(start).Round(time.Millisecond))
		}
	}

	once()
	last := digest(path)
	if !*quiet {
		fmt.Fprintf(os.Stderr, "ax: watching %s (ctrl-c to stop)\n", path)
	}
	for {
		last = waitForChange(path, last)
		once()
	}
}
