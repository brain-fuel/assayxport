package explorer

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestRenderEmbedsManifestAndFillsSlots(t *testing.T) {
	manifest := []byte(`{"index":{"schema_version":"2"},"shards":{}}`)
	for _, live := range []bool{false, true} {
		out := Render(manifest, live)
		if bytes.Contains(out, seedSlot) {
			t.Fatalf("live=%v: seed slot not replaced", live)
		}
		if bytes.Contains(out, liveSlot) {
			t.Fatalf("live=%v: live slot not replaced", live)
		}
		want := base64.StdEncoding.EncodeToString(manifest)
		if !bytes.Contains(out, []byte(want)) {
			t.Fatalf("live=%v: encoded manifest not embedded", live)
		}
		hasJS := bytes.Contains(out, []byte("EventSource"))
		if hasJS != live {
			t.Fatalf("live=%v: reload script presence = %v", live, hasJS)
		}
	}
}

func TestRenderDeterministic(t *testing.T) {
	manifest := []byte(`{"index":{},"shards":{}}`)
	if !bytes.Equal(Render(manifest, false), Render(manifest, false)) {
		t.Fatal("equal inputs produced different output")
	}
}
