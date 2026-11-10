package webapi

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lehlud/zentrale/domain"
)

func TestEventsLatestSkipsHistoryAndResumesFromWatermark(t *testing.T) {
	for _, count := range []int{0, 505} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			fixture := newAPIFixture(t)
			requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.org"})}`, nil))
			for range count {
				fixture.engine.Emit("historical", "", nil)
			}
			watermark, err := fixture.engine.Store.LatestEventID()
			if err != nil {
				t.Fatal(err)
			}
			headers := http.Header{}
			request, _ := http.NewRequest("GET", fixture.server.URL, nil)
			for _, cookie := range fixture.client.Jar.Cookies(request.URL) {
				headers.Add("Cookie", cookie.String())
			}
			connect := func(cursor string) *websocket.Conn {
				t.Helper()
				conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(fixture.server.URL, "http")+"/events?after="+cursor, headers)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = conn.Close() })
				_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				return conn
			}
			conn := connect("latest")
			var event domain.Event
			if err := conn.ReadJSON(&event); err != nil {
				t.Fatal(err)
			}
			if event.Type != "sync" || event.ID != watermark {
				t.Fatalf("expected one current watermark, got %+v", event)
			}
			fixture.engine.Emit("live", "", nil)
			for {
				if err := conn.ReadJSON(&event); err != nil {
					t.Fatal(err)
				}
				if event.Type == "response_reset" {
					continue
				}
				if event.Type != "live" || event.ID <= watermark {
					t.Fatalf("unexpected history or lost live update: %+v", event)
				}
				break
			}
			// Numeric cursors (including zero) must still replay missed events.
			resumed := connect(strconv.FormatInt(watermark, 10))
			var replay domain.Event
			if err := resumed.ReadJSON(&replay); err != nil {
				t.Fatal(err)
			}
			if replay.ID != event.ID || replay.Type != "live" {
				t.Fatalf("missed event was not replayed: %+v", replay)
			}
		})
	}
}
