// SPDX-License-Identifier: AGPL-3.0-only

package speedtest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// ndt7Server plays the M-Lab locate API and one ndt7 machine.
func ndt7Server(t *testing.T) *httptest.Server {
	t.Helper()
	up := websocket.Upgrader{Subprotocols: []string{ndt7Protocol}}
	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/locate", func(w http.ResponseWriter, r *http.Request) {
		ws := "ws" + strings.TrimPrefix(base, "http")
		json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{
			"machine": "mlab1-test01", "location": map[string]string{"city": "Testville", "country": "DE"},
			"urls": map[string]string{"ws:///ndt/v7/download": ws + "/ndt/v7/download", "ws:///ndt/v7/upload": ws + "/ndt/v7/upload"},
		}}})
	})
	mux.HandleFunc("/ndt/v7/download", func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		c.WriteMessage(websocket.TextMessage, []byte(`{"TCPInfo":{"MinRTT":12500}}`))
		buf := make([]byte, 1<<16)
		for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); {
			if c.WriteMessage(websocket.BinaryMessage, buf) != nil {
				return
			}
		}
		c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	})
	mux.HandleFunc("/ndt/v7/upload", func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		var got int64
		for {
			_, msg, err := c.ReadMessage()
			if err != nil {
				break
			}
			got += int64(len(msg))
			if got >= 4<<20 {
				break
			}
		}
		c.WriteMessage(websocket.TextMessage, []byte(`{"TCPInfo":{"BytesReceived":`+itoa(got)+`,"ElapsedTime":1000000}}`))
		c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	})
	ts := httptest.NewServer(mux)
	base = ts.URL
	t.Cleanup(ts.Close)
	return ts
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestNDT7MeasuresDownUpAndPing(t *testing.T) {
	ts := ndt7Server(t)
	res := NDT7{Locate: ts.URL + "/locate"}.Run(context.Background(), opts())
	if res.Err != "" {
		t.Fatal(res.Err)
	}
	if res.DownBps <= 0 || res.UpBps <= 0 || res.PingMS != 12.5 || !strings.Contains(res.Server, "Testville") || res.Bytes < 4<<20 {
		t.Errorf("%+v", res)
	}
}

func TestNDT7LocateFailureIsReported(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()
	res := NDT7{Locate: ts.URL}.Run(context.Background(), opts())
	if res.Err == "" || res.DownBps != 0 {
		t.Errorf("a failed locate must not produce numbers: %+v", res)
	}
}
