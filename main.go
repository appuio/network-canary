package main

import (
	"log"
	"time"

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
	l, err := newLogger()
	if err != nil {
		log.Fatalf("Failed to initialie logger: \n\n%s\n", err.Error())
	}
	l.Info("Starting canary..")

}

func newLogger() (logr.Logger, error) {
	cfg := zap.NewDevelopmentConfig()
	cfg.EncoderConfig.ConsoleSeparator = " | "
	z, err := cfg.Build()
	if err != nil {
		return logr.Logger{}, err
	}
	zap.ReplaceGlobals(z)

	logger := zapr.NewLogger(z)
	return logger.WithValues("version", version), nil
}
