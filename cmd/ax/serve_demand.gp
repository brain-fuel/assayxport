package main

import (
	"runtime"
	"strings"
	"sync"
	"time"

	"goforge.dev/assayxport/internal/explorer/loader"
	"goforge.dev/assayxport/internal/extract"
	"goforge.dev/assayxport/internal/schema"
)

// Demand-driven extraction: instead of parsing packages in a fixed file-walk
// order, `ax serve` parses the ones a client is actually looking at first. Each
// browser POSTs the level it is viewing to /api/focus; the server bumps every
// still-pending package under that path to the front of one shared parse queue.
//
// Multiple clients merge as a UNION, not a contest: extraction is a single,
// one-time, monotonic process (every package is parsed exactly once, then ready
// for everyone), so overlapping demand can only reorder the pending set, never
// conflict. A client's focus is dropped when it goes stale (focusTTL) so a closed
// tab stops skewing the order. The queue is goforge.dev/cadence's loader.Scheduler
// -- the very scheduler the browser runs per-client for shard loading, here run
// once server-side over the union of demand.

const focusTTL = 20 * time.Second

// focusRegistry holds each client's currently-viewed path. The union of the live
// (non-expired) paths is the demand signal the extraction dispatcher reads.
type focusRegistry struct {
	mu       sync.Mutex
	byClient map[string]focusEntry
	onChange func() // wakes the dispatcher when the union may have changed
	now      func() time.Time
}

type focusEntry struct {
	path string
	at   time.Time
}

func newFocusRegistry() *focusRegistry {
	return &focusRegistry{byClient: map[string]focusEntry{}, now: time.Now}
}

// set records (or refreshes) a client's focused path and signals a change.
func (fr *focusRegistry) set(client, path string) {
	fr.mu.Lock()
	fr.byClient[client] = focusEntry{path: path, at: fr.now()}
	onChange := fr.onChange
	fr.mu.Unlock()
	if onChange != nil {
		onChange()
	}
}

// drop removes a client's focus (e.g. its page closed) and signals a change.
func (fr *focusRegistry) drop(client string) {
	fr.mu.Lock()
	delete(fr.byClient, client)
	onChange := fr.onChange
	fr.mu.Unlock()
	if onChange != nil {
		onChange()
	}
}

// livePaths returns the deduplicated set of paths clients are focused on now,
// dropping entries older than focusTTL (a client that stopped heartbeating).
func (fr *focusRegistry) livePaths() []string {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	cutoff := fr.now().Add(-focusTTL)
	seen := map[string]bool{}
	var out []string
	for c, e := range fr.byClient {
		if e.at.Before(cutoff) {
			delete(fr.byClient, c)
			continue
		}
		if !seen[e.path] {
			seen[e.path] = true
			out = append(out, e.path)
		}
	}
	return out
}

// setOnChange installs the dispatcher wakeup for the duration of one assay.
func (fr *focusRegistry) setOnChange(fn func()) {
	fr.mu.Lock()
	fr.onChange = fn
	fr.mu.Unlock()
}

// underFocus reports whether a package at pkgPath sits at or below any focused
// path (segment-aware prefix; "" is the root and matches everything).
func underFocus(pkgPath string, focusPaths []string) bool {
	for _, fp := range focusPaths {
		if fp == "" || pkgPath == fp || strings.HasPrefix(pkgPath, fp+"/") {
			return true
		}
	}
	return false
}

// demandItem pairs a skeleton package with the extractor that parses it.
type demandItem struct {
	ext extract.DemandExtractor
	pkg schema.Package
}

// demandDrive parses every skeleton package of the demand extractors, ordering
// the pending set by the union of client focus (Visible) over a Distant baseline,
// via one shared loader.Scheduler. In-flight parses always finish; only the
// pending queue reorders, so a refocus takes effect within a few parses. A parse
// error on one file is recorded but does not abort the rest -- a single bad file
// should not blank the whole served tree.
func demandDrive(root string, demandExts []extract.DemandExtractor, fr *focusRegistry, emit func(schema.Package) error) error {
	items := map[string]demandItem{}
	for _, ext := range demandExts {
		pkgs, err := ext.Skeleton(root)
		if err != nil {
			return err
		}
		for _, p := range pkgs {
			items[p.ID] = demandItem{ext: ext, pkg: p}
		}
	}
	if len(items) == 0 {
		return nil
	}

	workers := runtime.NumCPU() - 1
	if workers < 1 {
		workers = 1
	}
	sched := loader.NewScheduler(workers)
	for id := range items {
		sched.Want(id, loader.Priority(loader.Distant))
	}

	workCh := make(chan demandItem, workers)
	doneCh := make(chan string, workers)
	errCh := make(chan error, len(items))
	wake := make(chan struct{}, 1)
	fr.setOnChange(func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	})
	defer fr.setOnChange(nil)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range workCh {
				out, err := it.ext.ExtractOne(root, it.pkg)
				if err == nil {
					err = emit(out)
				}
				if err != nil {
					errCh <- err
				}
				doneCh <- it.pkg.ID
			}
		}()
	}

	// The dispatcher owns sched (single-goroutine access, no lock needed). It
	// bumps focused pending packages, hands the scheduler's picks to workers, and
	// stops when every package has been parsed.
	finished := make(map[string]bool, len(items))
	applyFocus := func() {
		paths := fr.livePaths()
		if len(paths) == 0 {
			return
		}
		for id, it := range items {
			if !finished[id] && underFocus(it.pkg.Path, paths) {
				sched.Want(id, loader.Priority(loader.Visible))
			}
		}
	}
	pump := func() {
		for _, id := range sched.Next() {
			workCh <- items[id]
		}
	}

	remaining := len(items)
	applyFocus()
	pump()
	for remaining > 0 {
		select {
		case id := <-doneCh:
			finished[id] = true
			sched.Done(id)
			remaining--
			pump()
		case <-wake:
			applyFocus()
			pump()
		}
	}
	close(workCh)
	wg.Wait()

	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}
