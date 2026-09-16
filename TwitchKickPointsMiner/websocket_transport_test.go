package twitchchannelpointsminer

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

func TestKickWatchEventUsesJSONWebSocketFrame(t *testing.T) {
	received := make(chan map[string]interface{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()

		var payload map[string]interface{}
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

	if err := kickSendWatchEvent(ctx, conn, 123, 456); err != nil {
		t.Fatalf("send Kick watch event: %v", err)
	}

	select {
	case payload := <-received:
		if payload["type"] != "user_event" {
			t.Fatalf("event type got %v", payload["type"])
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for Kick watch event")
	}
}
