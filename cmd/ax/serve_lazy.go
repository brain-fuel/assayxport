package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
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

// snapshot is everything `ax serve` serves for one assay, built atomically so
// a re-assay swaps in a complete, consistent set rather than a half-updated
// mix. shell/embedded are the two page variants (lazy WASM vs. the inlined
// single-page fallback); indexJSON and shards are the lazy API bodies; combined
// is the /assayxport.json compatibility endpoint.
type snapshot struct {
	shell     []byte            // lazy page: index up front, shards on demand
	embedded  []byte            // whole manifest inlined (--no-wasm fallback)
	indexJSON []byte            // GET /api/index: the index plus couplings
	combined  []byte            // GET /assayxport.json: the combined manifest
	shards    map[string][]byte // GET /api/shard?path=... : one package's JSON
}

// buildSnapshot renders both page variants and pre-serializes the lazy API
// bodies for one (index, shards) assay. live carries the /__events reload
// subscription into whichever page is served.
func buildSnapshot(idx schema.Index, shards map[string]schema.Shard, live bool) (*snapshot, error) {
	combined, err := emit.Combined(idx, shards)
	if err != nil {
		return nil, err
	}
	indexJSON, err := indexPayload(idx, shards)
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
		shell:     explorer.Shell(live),
		embedded:  explorer.Render(combined, live),
		indexJSON: indexJSON,
		combined:  combined,
		shards:    sj,
	}, nil
}

// coupling is one aggregated inter-package call edge: the total weight of all
// internal/dynamic calls from package Src into package Dst of a given Kind. The
// lazy client lays out islands from these (the server has every shard, so it
// can precompute the coupling the client would otherwise need every shard to
// derive), and only then fetches a package's symbols on demand.
type coupling struct {
	Src  string `json:"src"`
	Dst  string `json:"dst"`
	Kind string `json:"kind"`
	W    int    `json:"w"`
}

// computeCouplings folds every internal/dynamic call edge into per-(src,dst,
// kind) weights, dropping self-edges (an island is not coupled to itself for
// layout). Output is sorted so /api/index is byte-stable for equal inputs.
func computeCouplings(idx schema.Index, shards map[string]schema.Shard) []coupling {
	type key struct{ src, dst, kind string }
	agg := map[key]int{}
	for _, pe := range idx.Packages {
		sh, ok := shards[pe.Shard]
		if !ok {
			continue
		}
		for _, s := range sh.Symbols {
			for _, c := range s.Calls {
				if (c.Kind != "internal" && c.Kind != "dynamic") || c.Ref == "" {
					continue
				}
				dst := c.Ref
				if i := strings.IndexByte(dst, '#'); i >= 0 {
					dst = dst[:i]
				}
				if dst == pe.ID {
					continue
				}
				agg[key{pe.ID, dst, c.Kind}] += c.Count
			}
		}
	}
	out := make([]coupling, 0, len(agg))
	for k, w := range agg {
		out = append(out, coupling{Src: k.src, Dst: k.dst, Kind: k.kind, W: w})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Src != out[j].Src {
			return out[i].Src < out[j].Src
		}
		if out[i].Dst != out[j].Dst {
			return out[i].Dst < out[j].Dst
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// indexPayload marshals the index with a "couplings" field appended, the body
// GET /api/index returns. The index itself is small (one entry per package);
// couplings add the inter-package layout graph without any per-symbol detail,
// so the client can draw and place every island before fetching a single shard.
func indexPayload(idx schema.Index, shards map[string]schema.Shard) ([]byte, error) {
	b, err := json.Marshal(idx)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	cb, err := json.Marshal(computeCouplings(idx, shards))
	if err != nil {
		return nil, err
	}
	m["couplings"] = cb
	return json.Marshal(m)
}
