package classes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestWritePubSubJSONUsesJSONWebSocketFrame(t *testing.T) {
	received := make(chan map[string]string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()

		var payload map[string]string
		if err := wsjson.Read(r.Context(), conn, &payload); err == nil {
			received <- payload
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial test websocket: %v", err)
	}
	defer conn.CloseNow()

	if err := writePubSubJSON(ctx, conn, map[string]string{"type": "PING"}); err != nil {
		t.Fatalf("send PubSub message: %v", err)
	}

	select {
	case payload := <-received:
		if payload["type"] != "PING" {
			t.Fatalf("message type got %q", payload["type"])
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for PubSub message")
	}
}
