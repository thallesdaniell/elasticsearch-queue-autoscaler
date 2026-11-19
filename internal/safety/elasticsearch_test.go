package safety

import (
	"context"
	"fmt"
	api "github.com/thallesdaniell/elasticsearch-queue-autoscaler/api/v1alpha1"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSafety(t *testing.T) {
	for _, tc := range []struct {
		name, health, fs, shards string
		bad                      bool
	}{
		{"healthy", `{"status":"green","number_of_nodes":1}`, `{"_nodes":{"failed":0},"nodes":{"a":{"roles":["data_hot"],"fs":{"data":[{"total_in_bytes":100,"available_in_bytes":50}]}}}}`, `[{"node":"a","state":"STARTED"}]`, false},
		{"red", `{"status":"red","number_of_nodes":1}`, `{}`, `[]`, true},
		{"relocating", `{"status":"green","number_of_nodes":1,"relocating_shards":1}`, `{}`, `[]`, true},
		{"full disk", `{"status":"green","number_of_nodes":1}`, `{"nodes":{"a":{"roles":["data"],"fs":{"data":[{"total_in_bytes":100,"available_in_bytes":1}]}}}}`, `[]`, true},
		{"missing stats", `{"status":"green","number_of_nodes":1}`, `{}`, `[]`, true},
		{"too many shards", `{"status":"green","number_of_nodes":1}`, `{"nodes":{"a":{"roles":["data"],"fs":{"data":[{"total_in_bytes":100,"available_in_bytes":50}]}}}}`, `[{"node":"a","state":"STARTED"},{"node":"a","state":"STARTED"}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "ApiKey test-key" {
					t.Error("missing auth")
				}
				switch r.URL.Path {
				case "/_cluster/health":
					fmt.Fprint(w, tc.health)
				case "/_nodes/stats/fs":
					fmt.Fprint(w, tc.fs)
				default:
					fmt.Fprint(w, tc.shards)
				}
			}))
			defer srv.Close()
			p := filepath.Join(t.TempDir(), "key")
			_ = os.WriteFile(p, []byte("test-key"), 0600)
			c := &Elasticsearch{Client: srv.Client(), APIKeyFile: p}
			e := c.Check(context.Background(), srv.URL, api.Safety{RequiredHealth: "green", MaxDiskUsedPercent: 90, MaxShardsPerNode: 1}, 1)
			if (e != nil) != tc.bad {
				t.Fatal(e)
			}
		})
	}
}
