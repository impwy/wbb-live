package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMediaCompletionMigrationPreservesLegacyAndRejectsRetrofittedProof(t *testing.T) {
	adminDSN := os.Getenv("WBB_TEST_PG_ADMIN_URL")
	if adminDSN == "" {
		t.Skip("requires disposable PostgreSQL admin URL")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatal("test admin connection failed")
	}
	t.Cleanup(admin.Close)
	var random [5]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "wbb_mig_" + hex.EncodeToString(random[:])
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal("create disposable database failed", err)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop disposable database: %v", err)
		}
	})
	u, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatal("invalid admin DSN")
	}
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal("disposable database connection failed")
	}
	t.Cleanup(pool.Close)
	entries, err := fs.Glob(migrationFS, "migrations/*.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(entries)
	for _, entry := range entries {
		if strings.Contains(entry, "0008_") {
			break
		}
		body, err := migrationFS.ReadFile(entry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("old migration %s: %v", entry, err)
		}
	}
	_, err = pool.Exec(ctx, `INSERT INTO users(id,email,display_name) VALUES('11111111-1111-4111-8111-111111111111','legacy@example.invalid','Legacy');
		INSERT INTO auth_session_families(id,user_id) VALUES('22222222-2222-4222-8222-222222222222','11111111-1111-4111-8111-111111111111');
		INSERT INTO auth_sessions(id,refresh_family_id,user_id,refresh_token_hash,expires_at) VALUES('33333333-3333-4333-8333-333333333333','22222222-2222-4222-8222-222222222222','11111111-1111-4111-8111-111111111111','test-only-hash',now()+interval '1 hour');
		INSERT INTO broadcasts(id,owner_id,title) VALUES('44444444-4444-4444-8444-444444444444','11111111-1111-4111-8111-111111111111','Legacy');
		INSERT INTO broadcast_sessions(id,broadcast_id,publisher_auth_family_id,initial_auth_session_id,source,state,prepare_deadline) VALUES('55555555-5555-4555-8555-555555555555','44444444-4444-4444-8444-444444444444','22222222-2222-4222-8222-222222222222','33333333-3333-4333-8333-333333333333','screen','stopping',now()+interval '1 hour');
		INSERT INTO publish_grants(jti,broadcast_session_id,auth_family_id,issued_at,expires_at) VALUES('66666666-6666-4666-8666-666666666666','55555555-5555-4555-8555-555555555555','22222222-2222-4222-8222-222222222222',now(),now()+interval '1 hour');
		INSERT INTO media_create_operations(id,broadcast_session_id,media_generation,grant_jti,state) VALUES
		('77777777-7777-4777-8777-777777777777','55555555-5555-4555-8555-555555555555',1,'66666666-6666-4666-8666-666666666666','pending'),
		('88888888-8888-4888-8888-888888888888','55555555-5555-4555-8555-555555555555',1,'66666666-6666-4666-8666-666666666666','closed');`)
	if err != nil {
		t.Fatal("seed old operations", err)
	}
	body, err := migrationFS.ReadFile("migrations/0008_media_completion.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatal("new migration", err)
	}
	rows, err := pool.Query(ctx, `SELECT state,completion_legacy,completion_epoch IS NULL,completion_evidence_kind IS NULL FROM media_create_operations ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for rows.Next() {
		var state string
		var legacy, noEpoch, noProof bool
		if err := rows.Scan(&state, &legacy, &noEpoch, &noProof); err != nil {
			t.Fatal(err)
		}
		if !legacy || !noEpoch || !noProof {
			t.Fatalf("old %s was reclassified", state)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if count != 2 {
		t.Fatalf("old operations=%d", count)
	}
	_, err = pool.Exec(ctx, `UPDATE media_create_operations SET completion_evidence_kind='never_sent' WHERE id='88888888-8888-4888-8888-888888888888'`)
	if err == nil {
		t.Fatal("old closed operation accepted retroactive proof")
	}
	_, err = pool.Exec(ctx, `UPDATE media_create_operations SET completion_contract_version='wbb-media-completion/1',completion_root='root',completion_slot='slot',completion_epoch='0123456789abcdef0123456789abcdef',completion_epoch_sequence=1,completion_source_path='wbb/55555555-5555-4555-8555-555555555555/1/source',completion_sdp_sha256='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' WHERE id='77777777-7777-4777-8777-777777777777'`)
	if err == nil {
		t.Fatal("old pending operation accepted retroactive tuple")
	}
}
