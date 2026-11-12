package outboxdelivery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/impwy/wbb-live/wbb-backend/internal/platform/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Opt-in process test. Only the exact disposable PostgreSQL container is
// stopped; the media/API PostgreSQL is never eligible for this test.
func TestOutboxProcessTermAndDatabaseRestart(t *testing.T) {
	if os.Getenv("WBB_TEST_PG_RESTART_CONTAINER") != "wbb-outbox-07c-pg" {
		t.Skip("disposable PostgreSQL restart not configured")
	}
	binary := os.Getenv("WBB_TEST_OUTBOX_BINARY")
	adminURL := os.Getenv("WBB_TEST_PG_ADMIN_URL")
	broker := os.Getenv("WBB_TEST_KAFKA_BROKERS")
	if binary == "" || adminURL == "" || broker == "" {
		t.Fatal("process integration environment incomplete")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal("admin connection failed")
	}
	t.Cleanup(admin.Close)
	random := make([]byte, 5)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	name := "wbb_outbox_test_" + hex.EncodeToString(random)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal("create database failed")
	}
	t.Cleanup(func() {
		c, k := context.WithTimeout(context.Background(), 6*time.Second)
		defer k()
		if _, err := admin.Exec(c, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop disposable database failed: %v", err)
		}
	})
	// A failed assertion while PostgreSQL is down still restores this exact
	// test container before the database cleanup runs.
	t.Cleanup(func() {
		c, k := context.WithTimeout(context.Background(), 15*time.Second)
		defer k()
		state, err := exec.CommandContext(c, "docker", "inspect", "-f", "{{.State.Running}}", "wbb-outbox-07c-pg").Output()
		if err != nil {
			t.Errorf("inspect disposable PostgreSQL failed")
			return
		}
		if strings.TrimSpace(string(state)) != "true" {
			if err := exec.CommandContext(c, "docker", "start", "wbb-outbox-07c-pg").Run(); err != nil {
				t.Errorf("restore disposable PostgreSQL failed")
				return
			}
		}
		for i := 0; i < 40; i++ {
			pingCtx, cancel := context.WithTimeout(c, time.Second)
			err := admin.Ping(pingCtx)
			cancel()
			if err == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Errorf("disposable PostgreSQL not ready for cleanup")
	})
	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatal("invalid admin URL")
	}
	u.Path = "/" + name
	pool, err := db.Connect(ctx, u.String())
	if err != nil {
		t.Fatal("test database unavailable")
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal("migration failed")
	}
	var bid string
	if err := pool.QueryRow(ctx, `WITH owner AS (INSERT INTO users(email,display_name) VALUES('outbox-process@example.invalid','Outbox Process') RETURNING id)
	 INSERT INTO broadcasts(owner_id,title) SELECT id,'Outbox Process' FROM owner RETURNING id::text`).Scan(&bid); err != nil {
		t.Fatal(err)
	}
	insert := func(version int, delay time.Duration) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO outbox_events(broadcast_id,aggregate_id,aggregate_version,type,payload,next_attempt_at)
		 VALUES($1,gen_random_uuid(),$2,'broadcast.session.starting','{"state":"starting"}',clock_timestamp()+$3::interval) RETURNING event_id::text`, bid, version, delay.String()).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	waitPublished := func(id string) {
		until := time.Now().Add(18 * time.Second)
		for time.Now().Before(until) {
			var published bool
			err := pool.QueryRow(ctx, `SELECT published_at IS NOT NULL FROM outbox_events WHERE event_id=$1`, id).Scan(&published)
			if err == nil && published {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("outbox process did not publish event")
	}
	start := func() *exec.Cmd {
		cmd := exec.Command(binary, "run")
		cmd.Env = append(os.Environ(), "WBB_DATABASE_URL="+u.String(), "WBB_KAFKA_BROKERS="+broker)
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	stop := func(cmd *exec.Cmd) {
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("SIGTERM exit: %v", err)
			}
		case <-time.After(6 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Fatal("SIGTERM did not exit")
		}
	}
	stopPG := func() {
		c, k := context.WithTimeout(ctx, 15*time.Second)
		defer k()
		if err := exec.CommandContext(c, "docker", "stop", "-t", "2", "wbb-outbox-07c-pg").Run(); err != nil {
			t.Fatal("stop disposable PostgreSQL failed")
		}
	}
	startPG := func() {
		c, k := context.WithTimeout(ctx, 15*time.Second)
		defer k()
		if err := exec.CommandContext(c, "docker", "start", "wbb-outbox-07c-pg").Run(); err != nil {
			t.Fatal("restart disposable PostgreSQL failed")
		}
		ready := false
		for i := 0; i < 100; i++ {
			pingCtx, pingCancel := context.WithTimeout(ctx, time.Second)
			err := pool.Ping(pingCtx)
			pingCancel()
			if err == nil {
				ready = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !ready {
			t.Fatal("disposable PostgreSQL did not recover")
		}
	}
	firstID := insert(1, 0)
	stopPG()
	first := start()
	t.Cleanup(func() {
		if first.ProcessState == nil {
			_ = first.Process.Kill()
			_ = first.Wait()
		}
	})
	time.Sleep(1100 * time.Millisecond)
	if err := first.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("dispatcher exited during initial DB outage")
	}
	startPG()
	waitPublished(firstID)
	secondID := insert(2, 3*time.Second)
	stop(first)
	var pending bool
	if err := pool.QueryRow(ctx, `SELECT published_at IS NULL FROM outbox_events WHERE event_id=$1`, secondID).Scan(&pending); err != nil || !pending {
		t.Fatalf("TERM did not preserve pending event: %v", err)
	}
	second := start()
	t.Cleanup(func() {
		if second.ProcessState == nil {
			_ = second.Process.Kill()
			_ = second.Wait()
		}
	})
	waitPublished(secondID)
	// This is the disposable PostgreSQL only. The running dispatcher must
	// survive broken connections and resume delivery after restart.
	stopPG()
	time.Sleep(1100 * time.Millisecond)
	startPG()
	thirdID := insert(3, 0)
	waitPublished(thirdID)
	if err := second.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("dispatcher process status: %v", err)
	}
	stop(second)
}
