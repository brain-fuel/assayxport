package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"goforge.dev/assayxport/internal/emit"
	"goforge.dev/assayxport/internal/explorer"
	"goforge.dev/assayxport/internal/schema"
)

// gzAsset serves a gzip-compressed embedded asset. Browsers all accept gzip,
// so the common path just sets Content-Encoding and writes the stored bytes
// (no per-request compression). A client that doesn't advertise gzip -- rare
// for this local dev tool -- gets the decompressed bytes instead.
func gzAsset(gz []byte, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(gz)
			return
		}
		zr, err := gzip.NewReader(bytes.NewReader(gz))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer zr.Close()
		_, _ = io.Copy(w, zr)
	}
}

// snapshot is everything `ax serve` serves for one assay, built atomically so a
// re-assay swaps in a complete, consistent set rather than a half-updated mix.
//
// Two shapes share this struct, chosen by serve mode:
//   - lazy (default, WASM): indexJSON is served up front; per-package shards are
//     read from dir on disk on demand. shardPaths is the set of valid
//     /api/shard?path= keys, so steady-state memory is the index plus a path set
//     regardless of repo size.
//   - --no-wasm (embedded): the whole manifest is inlined -- embedded is the
//     single self-contained page, combined the /assayxport.json blob, shards the
//     pre-marshaled per-package bodies. Held entirely in RAM; small trees only.
//
// The lazy page shell itself is assay-independent, so `ax serve` precomputes it
// once rather than storing it per snapshot.
type snapshot struct {
	indexJSON []byte // GET /api/index: the marshaled index (both modes)

	// lazy (disk-backed) fields
	dir        string          // generation dir holding assayxport.json + shards
	shardPaths map[string]bool // valid /api/shard?path= keys (traversal guard)

	// --no-wasm (in-RAM) fields
	embedded []byte            // whole manifest inlined page
	combined []byte            // GET /assayxport.json blob
	shards   map[string][]byte // GET /api/shard?path=... : one package's JSON
}

// writeAnalyzing responds while the first assay is still running: a 503 with a
// small JSON body and a Retry-After, so the WASM client polls until the index is
// ready instead of failing to boot.
func writeAnalyzing(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"status":"analyzing"}`))
}

// leanIndexPayload is the GET /api/index body: just the marshaled index. The
// hierarchical explorer lays every navigation level out from the index alone, so
// the index carries no per-symbol or inter-package detail -- a shard crosses the
// wire only when its package is opened. (Earlier versions appended an
// inter-package "couplings" graph for the old flat archipelago layout; the
// hierarchy replaced it, and the client now ignores it, so it is gone.)
func leanIndexPayload(idx schema.Index) ([]byte, error) {
	return json.Marshal(idx)
}

// buildSnapshot builds the in-RAM snapshot for --no-wasm (embedded) mode: it
// inlines the whole manifest into one page and pre-serializes the API bodies.
// This holds several manifest-sized copies at once, so it is used only for the
// small trees --no-wasm targets; lazy mode uses buildDiskSnapshot.
func buildSnapshot(idx schema.Index, shards map[string]schema.Shard, live bool) (*snapshot, error) {
	combined, err := emit.Combined(idx, shards)
	if err != nil {
		return nil, err
	}
	indexJSON, err := leanIndexPayload(idx)
	if err != nil {
		return nil, err
	}
	sj := make(map[string][]byte, len(shards))
	for path, sh := range shards {
		b, err := json.Marshal(sh)
		if err != nil {
			return nil, err
		}
		sj[path] = b
	}
	return &snapshot{
		indexJSON: indexJSON,
		embedded:  explorer.Render(combined, live),
		combined:  combined,
		shards:    sj,
	}, nil
}

// buildDiskSnapshot builds the lazy snapshot from an index whose shards already
// live under dir (written by assayToDir). It retains only the lean index and the
// set of valid shard paths (read from the index entries), so memory is
// independent of repo size. /api/shard streams each package's JSON from dir on
// demand.
func buildDiskSnapshot(idx schema.Index, dir string) (*snapshot, error) {
	indexJSON, err := leanIndexPayload(idx)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]bool, len(idx.Packages))
	for _, pe := range idx.Packages {
		paths[pe.Shard] = true
	}
	return &snapshot{indexJSON: indexJSON, dir: dir, shardPaths: paths}, nil
}

// streamCombined writes the /assayxport.json compatibility blob for disk mode by
// streaming the on-disk index and shard files -- {"index":<index>,"shards":{...}}
// -- one file at a time, so peak memory is a single shard rather than the whole
// manifest. Output is valid, deterministic (shards in sorted path order) JSON;
// it is compact rather than byte-identical to emit.Combined's indented form.
func streamCombined(w io.Writer, dir string, shardPaths map[string]bool) error {
	copyFile := func(name string) error {
		f, err := os.Open(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	}
	if _, err := io.WriteString(w, `{"index":`); err != nil {
		return err
	}
	if err := copyFile("assayxport.json"); err != nil {
		return err
	}
	if _, err := io.WriteString(w, `,"shards":{`); err != nil {
		return err
	}
	paths := make([]string, 0, len(shardPaths))
	for p := range shardPaths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for i, p := range paths {
		key, err := json.Marshal(p)
		if err != nil {
			return err
		}
		sep := ""
		if i > 0 {
			sep = ","
		}
		if _, err := io.WriteString(w, sep+string(key)+":"); err != nil {
			return err
		}
		if err := copyFile(p); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, `}}`)
	return err
}
