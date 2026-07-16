//go:build js && wasm

// Command axwasm is the explorer's browser-side data engine, compiled to
// WebAssembly. It fetches the manifest index once at boot, then answers the
// visualization's data questions -- give me the index, hydrate this package's
// shard, who calls this symbol, search for that -- out of an
// internal/explorer/graph.Engine, pulling each package's shard across the wire
// only when something first needs it.
//
// The page's canvas rendering stays in JavaScript (see
// internal/explorer/explorer.html); this program is purely the data layer.
// The two meet at window.axapi, a small object of functions this program
// installs:
//
//	axapi.index()            -> JSON string of the manifest index (sync; fetched at boot)
//	axapi.ensureShard(pkgID) -> Promise<JSON string> of that package's shard
//	axapi.callers(ref)       -> JSON string, inbound call edges known so far (sync)
//	axapi.search(q, limit)   -> JSON string, ranked matches over index + loaded shards (sync)
//
// Readiness is signalled by calling window.__axReady() (if defined) once the
// index has loaded and axapi is installed, so the page boots deterministically
// rather than polling.
package main

import (
	"encoding/json"
	"fmt"
	"syscall/js"
	"time"

	"goforge.dev/assayxport/internal/explorer/graph"
	"goforge.dev/assayxport/internal/explorer/loader"
	"goforge.dev/assayxport/internal/schema"
)

func main() {
	// Boot: fetch the index, build the engine, install axapi, signal ready.
	// A failure here leaves the page on its server-rendered shell with an
	// error in the console; there is nothing to render without an index.
	// The server binds its port before the first assay finishes, answering
	// /api/index with 503 while it works. Poll until the index is ready (or a
	// real network/HTTP error) rather than failing to boot, surfacing an
	// "analyzing" status to the page in the meantime.
	var body []byte
	for {
		b, status, err := fetchStatus("/api/index")
		if err != nil {
			reportBootError(fmt.Errorf("fetch index: %w", err))
			return
		}
		if status == 200 {
			body = b
			break
		}
		if status == 503 {
			reportStatus("analyzing")
			time.Sleep(400 * time.Millisecond)
			continue
		}
		reportBootError(fmt.Errorf("fetch index: status %d", status))
		return
	}
	reportStatus("")

	var idx schema.Index
	if err := json.Unmarshal(body, &idx); err != nil {
		reportBootError(fmt.Errorf("decode index: %w", err))
		return
	}

	eng := graph.New(idx, shardFetcher)
	install(eng, body)

	if ready := js.Global().Get("__axReady"); ready.Type() == js.TypeFunction {
		ready.Invoke()
	}

	// main returning would tear down the wasm instance and free the js.Funcs
	// the page still calls; block forever so axapi stays live for the page.
	select {}
}

// install publishes window.axapi. indexJSON is the raw index bytes fetched at
// boot, handed back verbatim by axapi.index() so the page parses exactly what
// the engine was built from.
func install(eng *graph.Engine, indexJSON []byte) {
	api := map[string]any{
		"index": js.FuncOf(func(js.Value, []js.Value) any {
			return string(indexJSON)
		}),
		"level": js.FuncOf(func(_ js.Value, args []js.Value) any {
			view, ok := eng.Level(arg(args, 0))
			if !ok {
				return `{"error":"unknown node"}`
			}
			return mustMarshal(view)
		}),
		"ensureShard": js.FuncOf(func(_ js.Value, args []js.Value) any {
			pkgID := arg(args, 0)
			return promise(func() (any, error) {
				sh, err := loadPkg(eng, pkgID)
				if err != nil {
					return nil, err
				}
				return marshal(sh)
			})
		}),
		// prefetch queues packages for background loading at Adjacent priority
		// (a sibling on the current level, likely-next) and kicks the driver.
		"prefetch": js.FuncOf(func(_ js.Value, args []js.Value) any {
			var ids []string
			_ = json.Unmarshal([]byte(arg(args, 0)), &ids)
			eng.Prefetch(ids, loader.Adjacent)
			drivePrefetch(eng)
			return nil
		}),
		"pin":      js.FuncOf(func(_ js.Value, args []js.Value) any { eng.PinPkg(arg(args, 0)); return nil }),
		"unpinAll": js.FuncOf(func(js.Value, []js.Value) any { eng.UnpinAll(); return nil }),
		// evict drops least-recently-used unpinned packages to fit the budget and
		// returns the evicted package ids so the page releases their symbols too.
		"evict": js.FuncOf(func(js.Value, []js.Value) any {
			ev := eng.Evict()
			if ev == nil {
				ev = []string{}
			}
			return mustMarshal(ev)
		}),
		"setBudget": js.FuncOf(func(_ js.Value, args []js.Value) any {
			if len(args) > 0 && args[0].Type() == js.TypeNumber {
				eng.SetBudget(int64(args[0].Int()))
			}
			return nil
		}),
		"callers": js.FuncOf(func(_ js.Value, args []js.Value) any {
			cs := eng.Callers(arg(args, 0))
			if cs == nil {
				cs = []graph.Caller{} // marshal an empty result as [] not null
			}
			return mustMarshal(cs)
		}),
		"search": js.FuncOf(func(_ js.Value, args []js.Value) any {
			limit := 0
			if len(args) > 1 && args[1].Type() == js.TypeNumber {
				limit = args[1].Int()
			}
			ms := eng.Search(arg(args, 0), limit)
			if ms == nil {
				ms = []graph.Match{} // marshal an empty result as [] not null
			}
			return mustMarshal(ms)
		}),
	}
	js.Global().Set("axapi", js.ValueOf(api))
}

// shardGate dedups concurrent loads of the same package: a background prefetch
// and a direct open racing for one package share a single fetch (the second
// waits on the first). The js runtime is single-threaded and cooperatively
// scheduled, so this map needs no lock -- only the channel handoff.
var shardGate = map[string]chan struct{}{}

// loadPkg loads a package's shard exactly once. If another goroutine is already
// loading it, this blocks on that load's completion channel and then returns the
// now-cached shard.
func loadPkg(eng *graph.Engine, pkgID string) (schema.Shard, error) {
	if ch, ok := shardGate[pkgID]; ok {
		<-ch
		return eng.EnsureShardForPkg(pkgID) // cached now
	}
	ch := make(chan struct{})
	shardGate[pkgID] = ch
	sh, err := eng.EnsureShardForPkg(pkgID)
	delete(shardGate, pkgID)
	close(ch)
	return sh, err
}

// drivePrefetch starts fetches for the scheduler's next packages, each of which
// re-drives on completion so freed slots pull the next-highest priority. Runs
// in the background off ensureShard/prefetch; on-demand opens never wait on it.
func drivePrefetch(eng *graph.Engine) {
	for _, pid := range eng.NextPrefetch() {
		go func(id string) {
			_, _ = loadPkg(eng, id)
			drivePrefetch(eng)
		}(pid)
	}
}

// shardFetcher is the engine's Fetcher: GET /api/shard?path=<shardPath>,
// decoded into a schema.Shard. It blocks the calling goroutine (an
// ensureShard promise's goroutine, never the main one) until the response
// arrives.
func shardFetcher(shardPath string) (schema.Shard, error) {
	body, err := fetchBytes("/api/shard?path=" + js.Global().Get("encodeURIComponent").Invoke(shardPath).String())
	if err != nil {
		return schema.Shard{}, err
	}
	var sh schema.Shard
	if err := json.Unmarshal(body, &sh); err != nil {
		return schema.Shard{}, fmt.Errorf("decode shard: %w", err)
	}
	return sh, nil
}

// fetchStatus performs a browser fetch of url and returns the response body and
// HTTP status. Unlike fetchBytes it does NOT treat a non-2xx status as an error,
// so callers can handle a 503 "analyzing" response and retry; only a network
// failure or a body-read failure returns a non-nil error (a non-ok response
// yields a nil body). It blocks the calling goroutine until the JS Promise chain
// settles and must not run on the main goroutine (that goroutine runs the js
// event loop the Promise resolves on); boot calls it before select{}, which is
// fine because the wasm runtime services promises during the blocking receive.
func fetchStatus(url string) ([]byte, int, error) {
	type result struct {
		data   []byte
		status int
		err    error
	}
	done := make(chan result, 1)

	var then, catch, bufThen, bufCatch js.Func
	then = js.FuncOf(func(_ js.Value, args []js.Value) any {
		resp := args[0]
		status := resp.Get("status").Int()
		if !resp.Get("ok").Bool() {
			// The status is all a retrying caller needs; skip reading the body.
			done <- result{status: status}
			return nil
		}
		bufThen = js.FuncOf(func(_ js.Value, a []js.Value) any {
			arr := js.Global().Get("Uint8Array").New(a[0])
			n := arr.Get("length").Int()
			b := make([]byte, n)
			js.CopyBytesToGo(b, arr)
			done <- result{data: b, status: status}
			return nil
		})
		bufCatch = js.FuncOf(func(_ js.Value, a []js.Value) any {
			done <- result{status: status, err: fmt.Errorf("GET %s: read body: %s", url, a[0].Call("toString").String())}
			return nil
		})
		resp.Call("arrayBuffer").Call("then", bufThen).Call("catch", bufCatch)
		return nil
	})
	catch = js.FuncOf(func(_ js.Value, args []js.Value) any {
		done <- result{err: fmt.Errorf("GET %s: %s", url, args[0].Call("toString").String())}
		return nil
	})
	defer func() {
		then.Release()
		catch.Release()
		if bufThen.Truthy() {
			bufThen.Release()
		}
		if bufCatch.Truthy() {
			bufCatch.Release()
		}
	}()

	js.Global().Call("fetch", url).Call("then", then).Call("catch", catch)
	r := <-done
	return r.data, r.status, r.err
}

// fetchBytes performs a browser fetch of url and returns the response body,
// erroring on any non-2xx status. It is the strict wrapper the shard fetcher
// uses; boot uses fetchStatus directly so it can retry on 503.
func fetchBytes(url string) ([]byte, error) {
	body, status, err := fetchStatus(url)
	if err != nil {
		return nil, err
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("GET %s: status %d", url, status)
	}
	return body, nil
}

// promise wraps fn in a JS Promise, running fn in its own goroutine so a
// blocking fetch inside it never stalls the js event loop. fn's string result
// resolves the promise; an error rejects it with a JS Error.
func promise(fn func() (any, error)) js.Value {
	handler := js.FuncOf(func(_ js.Value, args []js.Value) any {
		resolve, reject := args[0], args[1]
		go func() {
			v, err := fn()
			if err != nil {
				reject.Invoke(js.Global().Get("Error").New(err.Error()))
				return
			}
			resolve.Invoke(v)
		}()
		return nil
	})
	return js.Global().Get("Promise").New(handler)
}

func arg(args []js.Value, i int) string {
	if i >= len(args) || args[i].Type() != js.TypeString {
		return ""
	}
	return args[i].String()
}

func marshal(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// mustMarshal marshals v to a JSON string for a synchronous axapi call. The
// inputs are always plain data (slices of structs with JSON tags), so an
// encoding error is a programming bug; it surfaces as a JSON-encoded error
// object the page can detect rather than a thrown exception across the
// wasm/js boundary.
func mustMarshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(b)
}

func reportBootError(err error) {
	js.Global().Get("console").Call("error", "axwasm: "+err.Error())
	if cb := js.Global().Get("__axError"); cb.Type() == js.TypeFunction {
		cb.Invoke(err.Error())
	}
}

// reportStatus surfaces a transient boot status (e.g. "analyzing" while the
// server is still assaying) to the page via window.__axStatus, if defined. An
// empty msg clears it.
func reportStatus(msg string) {
	if cb := js.Global().Get("__axStatus"); cb.Type() == js.TypeFunction {
		cb.Invoke(msg)
	}
}
