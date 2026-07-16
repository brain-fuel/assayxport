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
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"goforge.dev/assayxport/internal/emit"
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

// current holds the latest rendered artifacts under a lock so requests
// always see a complete, consistent pair.
type current struct {
	mu       sync.RWMutex
	html     []byte
	manifest []byte
}

func (c *current) set(html, manifest []byte) {
	c.mu.Lock()
	c.html, c.manifest = html, manifest
	c.mu.Unlock()
}

func (c *current) get() (html, manifest []byte) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.html, c.manifest
}

func runServeCmd(args []string) error {
	fs2 := flag.NewFlagSet("ax serve", flag.ContinueOnError)
	port := fs2.Int("port", 7979, "port to listen on")
	noWatch := fs2.Bool("no-watch", false, "serve a single assay; do not re-assay on save")
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

	build := func() ([]byte, []byte, error) {
		idx, shards, err := assayOnce(path, langs, *quiet)
		if err != nil {
			return nil, nil, err
		}
		combined, err := emit.Combined(idx, shards)
		if err != nil {
			return nil, nil, err
		}
		return explorer.Render(combined, watch), combined, nil
	}

	html, manifest, err := build()
	if err != nil {
		return err
	}
	cur := &current{}
	cur.set(html, manifest)
	h := newHub()

	if watch {
		go func() {
			last := digest(path)
			for {
				last = waitForChange(path, last)
				start := time.Now()
				html, manifest, err := build()
				if err != nil {
					// A save mid-edit can be unparseable; keep serving the
					// last good assay and say why.
					fmt.Fprintln(os.Stderr, "ax: re-assay failed (serving last good):", err)
					continue
				}
				cur.set(html, manifest)
				h.broadcast()
				if !*quiet {
					fmt.Fprintf(os.Stderr, "ax: re-assayed in %s\n", time.Since(start).Round(time.Millisecond))
				}
			}
		}()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" && r.URL.Path != "/assayxport.html" {
			http.NotFound(w, r)
			return
		}
		page, _ := cur.get()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(page)
	})
	mux.HandleFunc("/assayxport.json", func(w http.ResponseWriter, r *http.Request) {
		_, m := cur.get()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(m)
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
		fmt.Fprintf(os.Stderr, "ax: exploring %s at http://%s (%s)\n", path, addr, mode)
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
