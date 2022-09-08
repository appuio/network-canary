package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"time"

	flag "github.com/spf13/pflag"

	"github.com/appuio/network-canary/ping"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
)

var (
	// these variables are populated by Goreleaser when releasing
	version = "unknown"
	commit  = "-dirty-"
	date    = time.Now().Format("2006-01-02")
)

func main() {
	listenAddress := flag.String("metrics-addr", ":2112", "Listen address for metrics server")
	dnsTargets := flag.StringSlice("ping-dns", []string{}, "List of DNS names to ping to")
	ipTargets := flag.StringSlice("ping-ip", []string{}, "List of IPs to ping to")
	source := flag.String("src", "", "The source address")

	update := flag.Duration("update-interval", 4*time.Second, "How often the canary should fetch DNS updates")
	interval := flag.Duration("ping-interval", time.Second, "How often the canary should send a ping to each target")
	timeout := flag.Duration("ping-timeout", 5*time.Second, "Timout until a ping should be considered lost")

	verbose := flag.Bool("verbose", false, "If the canary should log debug message")
	encoding := flag.String("encoding", "console", "How to format log output one of 'console' or 'json'")
	flag.Parse()

	if *source == "" {
		*source = os.Getenv("POD_IP")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	l, err := newLogger(*verbose, *encoding)
	if err != nil {
		log.Fatalf("Failed to initialie logger: \n\n%s\n", err.Error())
	}
	l.Info("Starting canary..")
	ctx = logr.NewContext(ctx, l)

	m := ping.NewManager(rttHist, ping.ManagerConfig{
		Source:         *source,
		DNSTargets:     *dnsTargets,
		IPTargets:      *ipTargets,
		UpdateInterval: *update,
		PingInterval:   *interval,
		PingTimeout:    *timeout,
	})
	go func() {
		err := m.Run(ctx)
		if err != nil {
			l.Error(err, "Ping manager failed")
			stop()
		}
	}()

	if err := serveMetrics(ctx, *listenAddress); err != nil {
		l.Error(err, "Serving Metrics service failed")
		os.Exit(1)
	}
}

func newLogger(debug bool, encoding string) (logr.Logger, error) {
	cfg := zap.NewProductionConfig()
	if debug {
		cfg.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	}
	cfg.Encoding = encoding
	cfg.EncoderConfig.ConsoleSeparator = " | "
	z, err := cfg.Build()
	if err != nil {
		return logr.Logger{}, err
	}
	zap.ReplaceGlobals(z)

	logger := zapr.NewLogger(z)
	return logger.WithValues("version", version), nil
}
