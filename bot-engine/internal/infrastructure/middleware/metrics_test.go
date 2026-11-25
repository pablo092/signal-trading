package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
)

// Dashboards and alerts select status classes with regexes such as "5..", so
// the label must be the numeric code, not the reason phrase.
func TestPrometheusMetricsUsesNumericStatusAndRouteTemplate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reg := prometheus.NewRegistry()
	r := gin.New()
	r.Use(PrometheusMetrics(reg))
	r.GET("/orders/:id", func(c *gin.Context) { c.Status(http.StatusServiceUnavailable) })

	for _, p := range []string{"/orders/1", "/orders/2", "/nope/secret-path"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}

	got := map[[2]string]float64{}
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "http_requests_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			var path, status string
			for _, l := range m.GetLabel() {
				switch l.GetName() {
				case "path":
					path = l.GetValue()
				case "status":
					status = l.GetValue()
				}
			}
			got[[2]string{path, status}] = m.GetCounter().GetValue()
		}
	}
	if got[[2]string{"/orders/:id", "503"}] != 2 {
		t.Fatalf("templated route with numeric status missing: %v", got)
	}
	if got[[2]string{"unknown", "404"}] != 1 {
		t.Fatalf("unmatched route must collapse to \"unknown\": %v", got)
	}
	if len(got) != 2 {
		t.Fatalf("raw paths must never become label values: %v", got)
	}
}
