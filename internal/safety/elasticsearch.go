// Package safety verifies Elasticsearch directly through its read-only APIs.
package safety

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	api "github.com/thallesdaniell/elasticsearch-queue-autoscaler/api/v1alpha1"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type Checker interface {
	Check(context.Context, string, api.Safety, int64) error
}
type Elasticsearch struct {
	Client     *http.Client
	APIKeyFile string
}

func New(caFile, keyFile string) (*Elasticsearch, error) {
	ca, e := os.ReadFile(caFile)
	if e != nil {
		return nil, e
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("invalid CA")
	}
	return &Elasticsearch{Client: &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, APIKeyFile: keyFile}, nil
}
func (c *Elasticsearch) get(ctx context.Context, endpoint, path string, out any) error {
	key, e := os.ReadFile(c.APIKeyFile)
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "GET", endpoint+path, nil)
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "ApiKey "+strings.TrimSpace(string(key)))
	res, e := c.Client.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("Elasticsearch HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(out)
}
func (c *Elasticsearch) Check(ctx context.Context, endpoint string, s api.Safety, expected int64) error {
	var h struct {
		Status       string `json:"status"`
		TimedOut     bool   `json:"timed_out"`
		Nodes        int64  `json:"number_of_nodes"`
		Relocating   int    `json:"relocating_shards"`
		Initializing int    `json:"initializing_shards"`
		Unassigned   int    `json:"unassigned_shards"`
	}
	if e := c.get(ctx, endpoint, "/_cluster/health", &h); e != nil {
		return e
	}
	if h.TimedOut || (h.Status != "green" && !(s.RequiredHealth == "yellow" && h.Status == "yellow")) || h.Nodes != expected || h.Relocating != 0 || h.Initializing != 0 {
		return fmt.Errorf("cluster not stable: health=%s nodes=%d relocating=%d initializing=%d", h.Status, h.Nodes, h.Relocating, h.Initializing)
	}
	var n struct {
		Summary struct {
			Failed int `json:"failed"`
		} `json:"_nodes"`
		Nodes map[string]struct {
			Roles []string `json:"roles"`
			FS    struct {
				Data []struct {
					Total     int64 `json:"total_in_bytes"`
					Available int64 `json:"available_in_bytes"`
				} `json:"data"`
			} `json:"fs"`
		} `json:"nodes"`
	}
	// Disk is evaluated per filesystem, rather than an average that can hide a full disk.
	if e := c.get(ctx, endpoint, "/_nodes/stats/fs", &n); e != nil {
		return e
	}
	if n.Summary.Failed != 0 || len(n.Nodes) != int(expected) {
		return fmt.Errorf("incomplete node statistics")
	}
	dataNodes := 0
	for _, node := range n.Nodes {
		if len(node.Roles) == 0 {
			return fmt.Errorf("missing node roles")
		}
		data := false
		for _, role := range node.Roles {
			if role == "data" || strings.HasPrefix(role, "data_") {
				data = true
			}
		}
		if !data {
			continue
		}
		dataNodes++
		if len(node.FS.Data) == 0 {
			return fmt.Errorf("missing data node filesystem")
		}
		for _, fs := range node.FS.Data {
			if fs.Total <= 0 || fs.Available < 0 || fs.Available > fs.Total || 100*(1-float64(fs.Available)/float64(fs.Total)) > float64(s.MaxDiskUsedPercent) {
				return fmt.Errorf("disk safety threshold exceeded")
			}
		}
	}
	if dataNodes == 0 {
		return fmt.Errorf("no data node statistics")
	}
	var shards []struct {
		Node  string `json:"node"`
		State string `json:"state"`
	}
	if e := c.get(ctx, endpoint, "/_cat/shards?format=json&h=node,state", &shards); e != nil {
		return e
	}
	counts := map[string]int{}
	for _, sh := range shards {
		if sh.State == "RELOCATING" || sh.State == "INITIALIZING" {
			return fmt.Errorf("shards moving")
		}
		if sh.Node != "" {
			counts[sh.Node]++
			if counts[sh.Node] > int(s.MaxShardsPerNode) {
				return fmt.Errorf("shard safety threshold exceeded")
			}
		}
	}
	return nil
}
