package server_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestUnknownRouteReturnsCoded404 验证未命中路由返回统一 404 信封（SYS0003）。
func TestUnknownRouteReturnsCoded404(t *testing.T) {
	ts := testServer(t)
	resp, err := http.Get(ts.URL + "/api/definitely-not-a-route")
	if err != nil {
		t.Fatalf("GET unknown route: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d want 404", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode 404 body: %v", err)
	}
	if body["error"] != "SYS0003" || body["status"] != float64(http.StatusNotFound) {
		t.Fatalf("body=%v", body)
	}
	if body["message"] != "接口不存在" {
		t.Fatalf("message=%v", body["message"])
	}
	if _, ok := body["ref"].(float64); !ok {
		t.Fatalf("ref 缺失: %v", body)
	}
}
