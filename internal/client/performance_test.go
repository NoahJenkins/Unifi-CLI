package client_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/config"
)

type performanceResource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func benchmarkConfig(b *testing.B, srv *httptest.Server) config.Config {
	b.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		b.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(u.Host)
	if err != nil {
		b.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		b.Fatal(err)
	}
	return config.Config{Host: host, Port: port, Insecure: true, Site: "default", Timeout: 30 * time.Second}
}

func newSilentTLSServer(handler http.Handler) *httptest.Server {
	srv := httptest.NewUnstartedServer(handler)
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	return srv
}

func BenchmarkOfficialCollection10000(b *testing.B) {
	const total = 10000
	srv := newSilentTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		count := 100
		if remaining := total - offset; remaining < count {
			count = remaining
		}
		items := make([]performanceResource, count)
		for index := range items {
			id := offset + index
			items[index] = performanceResource{ID: fmt.Sprintf("resource-%05d", id), Name: "Resource"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"offset": offset, "limit": 100, "count": count, "totalCount": total, "data": items})
	}))
	defer srv.Close()
	c, err := client.NewWithAPIKey(benchmarkConfig(b, srv), "synthetic-key", "benchmark")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		items, err := client.FetchOfficialAll[performanceResource](context.Background(), c, client.OfficialPath("resources"))
		if err != nil || len(items) != total {
			b.Fatalf("collection length=%d error=%v", len(items), err)
		}
	}
}

func BenchmarkOfficialDetailReads1000(b *testing.B) {
	const detailCount = 1000
	srv := newSilentTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(performanceResource{ID: r.URL.Path, Name: "Resource"})
	}))
	defer srv.Close()
	c, err := client.NewWithAPIKey(benchmarkConfig(b, srv), "synthetic-key", "benchmark")
	if err != nil {
		b.Fatal(err)
	}
	paths := make([]string, detailCount)
	for index := range paths {
		paths[index] = client.OfficialPath("resources", strconv.Itoa(index))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		jobs := make(chan string)
		errs := make(chan error, 4)
		var workers sync.WaitGroup
		for range 4 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for path := range jobs {
					var value performanceResource
					if err := c.DoOfficial(context.Background(), http.MethodGet, path, nil, &value); err != nil {
						errs <- err
						return
					}
				}
			}()
		}
		for _, path := range paths {
			jobs <- path
		}
		close(jobs)
		workers.Wait()
		close(errs)
		for err := range errs {
			b.Fatal(err)
		}
	}
}
