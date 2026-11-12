package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestInitialDatabaseRetryCanBeCancelled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	start := time.Now()
	pool, err := connectRun(ctx, "postgres://invalid:invalid@127.0.0.1:1/none", log)
	if pool != nil || err == nil || time.Since(start) > time.Second {
		t.Fatalf("startup retry did not stop promptly: pool=%v elapsed=%s", pool != nil, time.Since(start))
	}
}
