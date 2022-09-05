package main

import (
	"context"
	"net/http"
	"os"

	"github.com/go-logr/logr"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var rttHist = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "ping_rtt_seconds",
	Help:    "Round trip time of pings",
	Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 1},
}, []string{"source", "target", "reason"})

func serveMetrics(ctx context.Context, addr string) error {
	l := logr.FromContextOrDiscard(ctx)
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	http.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	prom := http.Server{
		Addr: addr,
	}
	go func() {
		<-ctx.Done()
		if err := prom.Shutdown(context.Background()); err != nil {
			l.Error(err, "Failed to stop metrics server")
			os.Exit(1)
		}
	}()
	err := prom.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
