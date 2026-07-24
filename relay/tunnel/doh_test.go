package tunnel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDoHResolver(t *testing.T) {
	// Mock DoH server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/dns-message" {
			http.Error(w, "bad content type", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/dns-message")
		// Dummy 12-byte DNS response header
		w.Write([]byte{0x00, 0x01, 0x81, 0x80, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00})
	}))
	defer mockServer.Close()

	doh := NewDoHResolver(nil)
	doh.servers = []string{mockServer.URL}

	dummyReq := []byte{0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	resp, err := doh.Resolve(context.Background(), dummyReq)
	if err != nil {
		t.Fatalf("expected DoH resolve to succeed, got error: %v", err)
	}

	if len(resp) < 12 {
		t.Fatalf("response too short: got %d bytes", len(resp))
	}
	if resp[0] != 0x12 || resp[1] != 0x34 {
		t.Errorf("transaction ID mismatch: got 0x%02x%02x, want 0x1234", resp[0], resp[1])
	}
}
