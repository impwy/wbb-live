package main

import (
	"context"
	"encoding/hex"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/impwy/wbb-live/wbb-backend/internal/mediacompletion"
	"github.com/impwy/wbb-live/wbb-backend/internal/mediaworker"
	"github.com/impwy/wbb-live/wbb-backend/internal/platform/db"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	secret := []byte(os.Getenv("WBB_MEDIA_SECRET"))
	if len(secret) < 32 {
		log.Error("WBB_MEDIA_SECRET missing")
		os.Exit(1)
	}
	key, err := hex.DecodeString(os.Getenv("WBB_COMPLETION_KEY_HEX"))
	if err != nil || len(key) != 32 {
		log.Error("WBB_COMPLETION_KEY_HEX invalid")
		os.Exit(1)
	}
	completion, err := mediacompletion.NewClient(os.Getenv("WBB_COMPLETION_URL"), key,
		os.Getenv("WBB_COMPLETION_ROOT"), os.Getenv("WBB_COMPLETION_SLOT"))
	if err != nil {
		log.Error("media completion configuration invalid")
		os.Exit(1)
	}
	var poolErr error
	for attempt := 0; attempt < 30; attempt++ {
		p, err := db.Connect(ctx, os.Getenv("WBB_DATABASE_URL"))
		if err == nil {
			defer p.Close()
			mtx := mediaworker.NewMTXClient(os.Getenv("WBB_MEDIA_API_URL"), os.Getenv("WBB_MEDIA_HLS_URL"), "control", mediaworker.ControlCredential(secret))
			mtx.WHIPBase = os.Getenv("WBB_MEDIA_WHIP_URL")
			worker := &mediaworker.Worker{DB: p, MTX: mtx, Completion: completion, Secret: secret,
				RTSPHost: os.Getenv("WBB_MEDIA_RTSP_HOST"), LockFile: os.Getenv("WBB_MEDIA_LOCK_FILE"), Log: log}
			if err := worker.Run(ctx); err != nil {
				log.Error("media worker stopped", "error", err)
				os.Exit(1)
			}
			return
		}
		poolErr = err
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
	log.Error("database unavailable", "error", poolErr)
	os.Exit(1)
}
