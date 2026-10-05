package httpapi

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type routeMetric struct {
	Count, Failures uint64
	Sum             float64
	Buckets         [7]uint64
}

var metricMu sync.Mutex
var metrics = map[string]*routeMetric{}
var boundaries = []float64{.005, .01, .025, .05, .15, 1, 5}

type recordedWriter struct {
	http.ResponseWriter
	status int
}

func (w *recordedWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }
func (w *recordedWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func (w *recordedWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.(http.Hijacker).Hijack()
}
func recordRoute(path string, elapsed time.Duration, status int) {
	route := "other"
	for _, name := range []string{"checkpoint", "journalentries", "renew_lease", "complete", "cancel", "encrypt", "decrypt", "export", "import", "events/batch", "events"} {
		if strings.HasSuffix(path, "/"+name) {
			route = name
			break
		}
	}
	metricMu.Lock()
	defer metricMu.Unlock()
	m := metrics[route]
	if m == nil {
		m = &routeMetric{}
		metrics[route] = m
	}
	m.Count++
	if status >= 400 {
		m.Failures++
	}
	seconds := elapsed.Seconds()
	m.Sum += seconds
	for i, b := range boundaries {
		if seconds <= b {
			m.Buckets[i]++
		}
	}
}
func (a *API) serveMetrics(w http.ResponseWriter, r *http.Request) {
	metricMu.Lock()
	defer metricMu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintln(w, "# TYPE pulumid_http_requests_total counter")
	fmt.Fprintln(w, "# TYPE pulumid_http_duration_seconds histogram")
	for route, m := range metrics {
		fmt.Fprintf(w, "pulumid_http_requests_total{route=%q} %d\npulumid_http_errors_total{route=%q} %d\n", route, m.Count, route, m.Failures)
		for i, b := range boundaries {
			fmt.Fprintf(w, "pulumid_http_duration_seconds_bucket{route=%q,le=%q} %d\n", route, fmt.Sprint(b), m.Buckets[i])
		}
		fmt.Fprintf(w, "pulumid_http_duration_seconds_bucket{route=%q,le=\"+Inf\"} %d\npulumid_http_duration_seconds_sum{route=%q} %g\npulumid_http_duration_seconds_count{route=%q} %d\n", route, m.Count, route, m.Sum, route, m.Count)
	}
}
