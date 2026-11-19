package publication

import (
	"net/http"
	"testing"
)

// A bundle upload must not have a whole-request deadline: a large bundle on a
// slow link would be cut off mid-body (the v0.25.0 two-minute timeout).
func TestUploadClientHasNoWholeRequestDeadline(t *testing.T) {
	client := CentralClient{}.uploadClient()
	if client.Timeout != 0 {
		t.Fatalf("upload client timeout = %v, want none", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.ResponseHeaderTimeout == 0 || transport.TLSHandshakeTimeout == 0 {
		t.Fatalf("upload client must still bound the handshake and Central's answer: %#v", client.Transport)
	}
	if (CentralClient{}).client().Timeout == 0 {
		t.Fatal("status and promotion requests keep their deadline")
	}
}
