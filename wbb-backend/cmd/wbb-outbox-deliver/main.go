package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/impwy/wbb-live/wbb-backend/internal/outboxdelivery"
	"github.com/impwy/wbb-live/wbb-backend/internal/platform/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("outbox command failed", "code", err.Error())
		os.Exit(1)
	}
}
func run(log *slog.Logger) error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage_run_stats_quarantine_requeue")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	dsn := os.Getenv("WBB_DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("database_url_missing")
	}
	var pool *pgxpool.Pool
	var err error
	if os.Args[1] == "run" {
		pool, err = connectRun(ctx, dsn, log)
		if ctx.Err() != nil {
			return nil
		}
	} else {
		connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		pool, err = db.Connect(connectCtx, dsn)
		cancel()
	}
	if err != nil {
		return fmt.Errorf("database_unavailable")
	}
	defer pool.Close()
	store := outboxdelivery.PostgresStore{DB: pool}
	switch os.Args[1] {
	case "run":
		brokers := os.Getenv("WBB_KAFKA_BROKERS")
		if brokers == "" {
			return fmt.Errorf("kafka_brokers_missing")
		}
		p, err := outboxdelivery.NewKafkaProducer(brokers)
		if err != nil {
			return fmt.Errorf("kafka_brokers_invalid")
		}
		defer p.Close()
		log.Info("outbox dispatcher started", "topic", outboxdelivery.Topic)
		return (&outboxdelivery.Dispatcher{Store: store, Producer: p,
			OnErrorCode: func(code string) { log.Warn("outbox delivery retry", "code", code) },
		}).Run(ctx)
	case "stats":
		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		st, err := store.Stats(queryCtx)
		if err != nil {
			return fmt.Errorf("stats_unavailable")
		}
		if err := json.NewEncoder(os.Stdout).Encode(st); err != nil {
			return fmt.Errorf("output_unavailable")
		}
		return nil
	case "quarantine":
		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if len(os.Args) > 3 {
			return fmt.Errorf("quarantine_one_event_id_only")
		}
		if len(os.Args) == 3 {
			if err := outboxdelivery.ValidateID(os.Args[2]); err != nil {
				return fmt.Errorf("quarantine_event_id_invalid")
			}
			item, err := store.QuarantinedByID(queryCtx, os.Args[2])
			if err != nil {
				return fmt.Errorf("quarantine_lookup_unavailable")
			}
			if item == nil {
				return fmt.Errorf("quarantine_event_not_found")
			}
			if err := json.NewEncoder(os.Stdout).Encode(item); err != nil {
				return fmt.Errorf("output_unavailable")
			}
			return nil
		}
		items, err := store.Quarantined(queryCtx)
		if err != nil {
			return fmt.Errorf("quarantine_list_unavailable")
		}
		if err := json.NewEncoder(os.Stdout).Encode(items); err != nil {
			return fmt.Errorf("output_unavailable")
		}
		return nil
	case "requeue":
		if len(os.Args) != 3 {
			return fmt.Errorf("requeue_event_id_required")
		}
		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		changed, err := store.Requeue(queryCtx, os.Args[2])
		if err != nil {
			if err := outboxdelivery.ValidateID(os.Args[2]); err != nil {
				return fmt.Errorf("requeue_event_id_invalid")
			}
			return fmt.Errorf("requeue_unavailable")
		}
		if !changed {
			return fmt.Errorf("requeue_not_quarantined_unpublished")
		}
		log.Info("outbox event requeued", "event_id", os.Args[2])
		return nil
	default:
		return fmt.Errorf("unknown_command")
	}
}

func connectRun(ctx context.Context, dsn string, log *slog.Logger) (*pgxpool.Pool, error) {
	delay := time.Second
	for ctx.Err() == nil {
		attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		pool, err := db.Connect(attemptCtx, dsn)
		cancel()
		if err == nil {
			return pool, nil
		}
		log.Warn("outbox database connection retry", "code", "database_unavailable")
		wait := delay + time.Duration(rand.Int63n(int64(delay/2+1)))
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		if delay < 8*time.Second {
			delay *= 2
		}
	}
	return nil, ctx.Err()
}
