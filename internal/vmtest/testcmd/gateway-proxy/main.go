package main

import (
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() {
	target := strings.TrimSpace(os.Getenv("KATL_VMTEST_BACKEND_URL"))
	if target == "" {
		target = "http://echo.katl-vmtest.svc.cluster.local:8080"
	}
	backend, err := url.Parse(target)
	if err != nil {
		log.Fatal(err)
	}
	client := http.Client{Timeout: 2 * time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		proxyURL := *backend
		proxyURL.Path = joinPath(backend.Path, r.URL.Path)
		proxyURL.RawQuery = r.URL.RawQuery
		req, err := http.NewRequestWithContext(r.Context(), r.Method, proxyURL.String(), nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("X-Katl-VMTest-Gateway", "envoy-gateway-fixture")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}

func joinPath(base, path string) string {
	base = strings.TrimRight(base, "/")
	if base == "" {
		base = "/"
	}
	path = "/" + strings.TrimLeft(path, "/")
	if base == "/" {
		return path
	}
	return base + path
}
