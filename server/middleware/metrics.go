package middleware

import (
	"context"
	"net/http"
	"time"

	prom "go.opentelemetry.io/otel/exporters/metric/prometheus"
	"go.opentelemetry.io/otel/label"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric/controller/pull"
	"go.opentelemetry.io/otel/sdk/resource"
)

type statusRecorder struct {
	http.ResponseWriter
	Status    int
	StartTime time.Time
}

func (r *statusRecorder) WriteHeader(status int) {
	r.Status = status
	r.ResponseWriter.WriteHeader(status)
}

func PrometheusHandler(hostHeader string) (func(http.Handler) http.Handler, http.HandlerFunc, error) {
	res, err := resource.New(
		context.Background(),
	)
	if err != nil {
		return nil, nil, err
	}

	// Create a meter
	exporter, err := prom.NewExportPipeline(
		prom.Config{
			DefaultHistogramBoundaries: []float64{0.1, 0.3, 1.2, 5.0},
		},
		pull.WithResource(res),
	)
	if err != nil {
		return nil, nil, err
	}

	meter := exporter.MeterProvider().Meter("request")
	ctx := context.Background()

	// Use two instruments
	counterTotalRequests := metric.Must(meter).NewInt64Counter(
		"http_request_counts",
		metric.WithDescription("Total requests"),
	)
	recorder := metric.Must(meter).NewFloat64ValueRecorder(
		"http_request_duration_seconds",
		metric.WithDescription("Request durations"),
	)

	h := func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			ww := &statusRecorder{
				ResponseWriter: w,
				Status:         200,
				StartTime:      time.Now(),
			}

			next.ServeHTTP(ww, r)
			d := time.Since(ww.StartTime)
			counterTotalRequests.Add(ctx, 1,
				label.String("destination", r.Header.Get(hostHeader)),
				label.Int("code", ww.Status),
			)
			recorder.Record(ctx, d.Seconds())
		}
		return http.HandlerFunc(fn)
	}

	return h, exporter.ServeHTTP, nil
}
