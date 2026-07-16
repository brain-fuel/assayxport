// Package explorer renders the interactive manifest explorer: a single
// self-contained HTML file with the combined manifest embedded, viewable
// offline in any browser with no server and no external assets.
//
// The manifest is embedded base64-encoded rather than as raw JSON. A
// multi-megabyte JSON blob inside a <script> tag passes through the
// browser's HTML tokenizer before JSON.parse ever sees it, and that path
// has edge cases (script-data states, transport quirks) that can corrupt
// the payload. Base64's alphabet contains nothing the tokenizer can
// misread, so the same bytes that were encoded are the bytes decoded.
package explorer

import (
	"bytes"
	_ "embed"
	"encoding/base64"
)

//go:embed explorer.html
var tmpl []byte

// seedSlot is replaced with the base64 of the combined manifest.
// liveSlot is replaced with the live-reload script by `ax serve`
// (watch mode) and left inert by `ax assay`.
var (
	seedSlot = []byte("/*SEED*/")
	liveSlot = []byte("/*LIVE*/")
)

// liveJS reloads the page when the server signals that the manifest
// changed. It is injected only by `ax serve` in watch mode; the static
// artifact written by `ax assay` never carries it.
const liveJS = `(function(){try{var es=new EventSource("/__events");` +
	`es.addEventListener("reload",function(){location.reload()});}catch(e){}})();`

// Render returns the explorer HTML with the combined manifest (the exact
// bytes emit.Combined produces) embedded. When live is true the page also
// subscribes to /__events and reloads itself when the server emits a
// "reload" event.
//
// Output is deterministic for equal inputs when live is fixed: the
// template is embedded at compile time and base64 is a pure function.
func Render(combinedManifest []byte, live bool) []byte {
	b64 := base64.StdEncoding.EncodeToString(combinedManifest)
	out := bytes.Replace(tmpl, seedSlot, []byte(b64), 1)
	js := ""
	if live {
		js = liveJS
	}
	return bytes.Replace(out, liveSlot, []byte(js), 1)
}
