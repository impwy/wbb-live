package mediaworker

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/impwy/wbb-live/wbb-backend/internal/platform/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Worker struct {
	DB                 *pgxpool.Pool
	MTX                *MTXClient
	Secret             []byte
	RTSPHost, LockFile string
	Log                *slog.Logger
	incarnation        string
	lockFile           *os.File
	runs               map[string]*running
	lostLease          map[string]bool
	absentSince        map[string]time.Time
}

type running struct {
	process           *Process
	runID             string
	fence, generation int64
}
type sessionRow struct {
	id, bid, state       string
	generation           int64
	deadline             time.Time
	firstIngress, lostAt *time.Time
}

func randomUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func credential(secret []byte, kind, id string) string {
	m := hmac.New(sha256.New, secret)
	_, _ = m.Write([]byte(kind + ":" + id))
	return hex.EncodeToString(m.Sum(nil))
}

func ControlCredential(secret []byte) string { return credential(secret, "control", "api") }

func (w *Worker) Run(ctx context.Context) error {
	if len(w.Secret) < 32 || w.DB == nil || w.MTX == nil || w.MTX.WHIPBase == "" || w.RTSPHost == "" || w.LockFile == "" {
		return fmt.Errorf("incomplete worker configuration")
	}
	if w.Log == nil {
		w.Log = slog.Default()
	}
	lock, err := os.OpenFile(w.LockFile, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another media worker holds lock: %w", err)
	}
	// Do not call LOCK_UN: FFmpeg inherited this open file description and an
	// explicit unlock here would also release its proof-of-life lock.
	w.lockFile = lock
	w.incarnation = randomUUID()
	w.runs = make(map[string]*running)
	w.lostLease = make(map[string]bool)
	w.absentSince = make(map[string]time.Time)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		err := w.tick(ctx)
		if err != nil {
			w.Log.Error("media worker cycle", "error", err)
			w.stopAll()
		}
		select {
		case <-ctx.Done():
			w.stopAll()
			return nil
		case <-ticker.C:
		}
	}
}

func (w *Worker) renewLeases(ctx context.Context) error {
	var sql strings.Builder
	sql.WriteString(`UPDATE media_worker_runs r SET lease_until=now()+interval '30 seconds',updated_at=now() FROM (VALUES `)
	var args []any
	expected := make(map[string]string)
	for sid, run := range w.runs {
		if w.lostLease[sid] {
			continue
		}
		if len(expected) != 0 {
			sql.WriteByte(',')
		}
		args = append(args, run.runID, run.fence)
		sql.WriteString(fmt.Sprintf("($%d::uuid,$%d::bigint)", len(args)-1, len(args)))
		expected[run.runID] = sid
	}
	if len(expected) == 0 {
		return nil
	}
	args = append(args, w.incarnation)
	sql.WriteString(fmt.Sprintf(`) AS expected(run_id,fence)
		WHERE r.run_id=expected.run_id AND r.fencing_token=expected.fence
		AND r.worker_incarnation=$%d::uuid AND r.state='running' AND r.lease_until>now()
		RETURNING r.run_id::text`, len(args)))
	leaseCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	rows, err := w.DB.Query(leaseCtx, sql.String(), args...)
	if err != nil {
		cancel()
		return err // shared DB trust failure: stop every controlled process
	}
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			rows.Close()
			cancel()
			return err
		}
		delete(expected, runID)
	}
	err = rows.Err()
	rows.Close()
	cancel()
	if err != nil {
		return err
	}
	for _, sid := range expected {
		w.lostLease[sid] = true
		w.Log.Warn("worker lease expired or fencing lost", "session", sid)
	}
	return nil
}

func (w *Worker) terminateLostRuns(ctx context.Context) error {
	for sid, run := range w.runs {
		if !w.lostLease[sid] {
			continue
		}
		term, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		err := run.process.Terminate(term)
		cancel()
		if err != nil || run.process.GroupAlive() {
			w.Log.Error("unverified FFmpeg termination", "session", sid, "error", err)
		} else {
			delete(w.runs, sid)
		}
		// Slow, failing per-session termination cannot starve healthy leases.
		if err := w.renewLeases(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) stopAll() {
	for sid, run := range w.runs {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		if err := run.process.Terminate(ctx); err != nil {
			w.Log.Error("terminate FFmpeg", "session", sid, "error", err)
			cancel()
			w.lostLease[sid] = true
			continue
		}
		cancel()
		if !run.process.GroupAlive() {
			delete(w.runs, sid)
			w.lostLease[sid] = true
		}
	}
}

func (w *Worker) tick(ctx context.Context) error {
	if err := w.renewLeases(ctx); err != nil {
		return err
	}
	if err := w.terminateLostRuns(ctx); err != nil {
		return err
	}
	snapCtx, snapCancel := context.WithTimeout(ctx, 12*time.Second)
	snapshot, err := w.MTX.Snapshot(snapCtx)
	snapCancel()
	if err != nil {
		return err
	}
	if err := w.renewLeases(ctx); err != nil {
		return err
	}
	queryCtx, queryCancel := context.WithTimeout(ctx, 4*time.Second)
	rows, err := w.DB.Query(queryCtx, `SELECT id::text,broadcast_id::text,state,media_generation,prepare_deadline,first_ingress_at,ingress_lost_at FROM broadcast_sessions WHERE state IN ('starting','live','stopping') ORDER BY created_at`)
	if err != nil {
		queryCancel()
		return err
	}
	var sessions []sessionRow
	for rows.Next() {
		var s sessionRow
		if err := rows.Scan(&s.id, &s.bid, &s.state, &s.generation, &s.deadline, &s.firstIngress, &s.lostAt); err != nil {
			rows.Close()
			queryCancel()
			return err
		}
		sessions = append(sessions, s)
	}
	err = rows.Err()
	rows.Close()
	queryCancel()
	if err != nil {
		return err
	}
	if err := w.renewLeases(ctx); err != nil {
		return err
	}
	for _, s := range sessions {
		if w.lostLease[s.id] {
			transitionCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
			err := w.transition(transitionCtx, s, "stopping", "worker_lease_lost", 0)
			cancel()
			if err != nil {
				w.Log.Error("lease-loss transition deferred", "session", s.id, "error", err)
				if err := w.renewLeases(ctx); err != nil {
					return err
				}
				continue
			}
			delete(w.lostLease, s.id)
			s.state = "stopping"
		}
		sessionCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		err := w.reconcile(sessionCtx, s, snapshot)
		cancel()
		if err != nil {
			w.Log.Error("media session reconciliation", "session", s.id, "error", err)
			if s.state == "stopping" {
				// Keep its resources and retry this session next cycle; unrelated
				// LIVE sessions retain their own FFmpeg processes and leases.
				if err := w.renewLeases(ctx); err != nil {
					return err
				}
				continue
			}
			transitionCtx, transitionCancel := context.WithTimeout(ctx, 4*time.Second)
			transitionErr := w.transition(transitionCtx, s, "stopping", "media_error", 0)
			transitionCancel()
			if transitionErr != nil {
				w.Log.Error("session failure transition deferred", "session", s.id, "error", transitionErr)
				w.lostLease[s.id] = true
				if err := w.renewLeases(ctx); err != nil {
					return err
				}
				continue
			}
			// Cleanup starts next cycle with a fresh MediaMTX snapshot. This also
			// bounds the gap before healthy sessions renew their leases.
		}
		if err := w.renewLeases(ctx); err != nil {
			return err
		}
	}
	return nil
}

func paths(s sessionRow) (string, string) {
	return fmt.Sprintf("wbb/%s/%d/source", s.id, s.generation), fmt.Sprintf("wbb/%s/%d/720p", s.id, s.generation)
}

func sessionRTSP(snap MediaSnapshot, source, output string) []MTXSession {
	var connections []MTXSession
	for _, conn := range snap.RTSP {
		if conn.Path == source || conn.Path == output {
			connections = append(connections, conn)
		}
	}
	return connections
}

func (w *Worker) reconcile(ctx context.Context, s sessionRow, snap MediaSnapshot) error {
	source, output := paths(s)
	srcReady, outReady := snap.Paths[source].Ready, snap.Paths[output].Ready
	if s.state == "stopping" {
		return w.cleanup(ctx, s, snap)
	}
	if srcReady {
		_, err := w.DB.Exec(ctx, `UPDATE broadcast_sessions SET first_ingress_at=COALESCE(first_ingress_at,now()),ingress_lost_at=NULL WHERE id=$1 AND state IN ('starting','live')`, s.id)
		if err != nil {
			return err
		}
	} else if s.firstIngress != nil {
		_, err := w.DB.Exec(ctx, `UPDATE broadcast_sessions SET ingress_lost_at=COALESCE(ingress_lost_at,now()) WHERE id=$1 AND state IN ('starting','live')`, s.id)
		if err != nil {
			return err
		}
		if s.lostAt != nil && time.Since(*s.lostAt) >= 15*time.Second {
			return w.transition(ctx, s, "stopping", "ingress_lost", 0)
		}
	}
	if s.state == "starting" && time.Now().After(s.deadline) {
		return w.transition(ctx, s, "stopping", "prepare_timeout", 0)
	}
	run := w.runs[s.id]
	if run != nil && !run.process.GroupAlive() {
		delete(w.runs, s.id)
		return w.transition(ctx, s, "stopping", "transcoder_exited", run.fence)
	}
	if run == nil && srcReady {
		var attached bool
		if err := w.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_create_operations WHERE broadcast_session_id=$1 AND state='attached' AND media_generation=$2)`, s.id, s.generation).Scan(&attached); err != nil {
			return err
		}
		if attached {
			// A previous fenced FFmpeg can leave an RTSP reader even when its
			// output path is not ready. Clear both paths before a new run.
			if stale := sessionRTSP(snap, source, output); len(stale) != 0 {
				for _, conn := range stale {
					if err := w.MTX.Kick(ctx, "rtspsessions", conn.ID); err != nil {
						return err
					}
				}
				return nil
			}
			var err error
			run, err = w.start(ctx, s, outReady)
			if err != nil {
				return err
			}
		}
	}
	if s.state == "starting" && run != nil && srcReady && outReady {
		user, pass := workerIdentity(w.Secret, run)
		ready, err := w.MTX.PartReady(ctx, output, user, pass)
		if err != nil {
			return err
		}
		if ready {
			return w.transition(ctx, s, "live", "", run.fence)
		}
	}
	return nil
}

func workerIdentity(secret []byte, run *running) (string, string) {
	f := strconv.FormatInt(run.fence, 10)
	return "worker." + run.runID + "." + f, credential(secret, "worker", run.runID+":"+f)
}

func (w *Worker) start(ctx context.Context, s sessionRow, oldOutputReady bool) (*running, error) {
	tx, err := w.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var state string
	var gen, fence int64
	if err := tx.QueryRow(ctx, `SELECT state,media_generation,media_fencing_token FROM broadcast_sessions WHERE id=$1 FOR UPDATE`, s.id).Scan(&state, &gen, &fence); err != nil {
		return nil, err
	}
	if (state != "starting" && state != "live") || gen != s.generation {
		return nil, nil
	}
	var previousState string
	var oldLease time.Time
	var inherited bool
	err = tx.QueryRow(ctx, `SELECT state,lease_until,lock_inherited FROM media_worker_runs WHERE broadcast_session_id=$1 FOR UPDATE`, s.id).Scan(&previousState, &oldLease, &inherited)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil && previousState != "exited" && (!inherited || time.Now().Before(oldLease) || oldOutputReady) {
		return nil, nil
	}
	fence++
	if _, err := tx.Exec(ctx, `UPDATE broadcast_sessions SET media_fencing_token=$2 WHERE id=$1`, s.id, fence); err != nil {
		return nil, err
	}
	runID := randomUUID()
	_, err = tx.Exec(ctx, `INSERT INTO media_worker_runs(broadcast_session_id,media_generation,fencing_token,worker_incarnation,run_id,lease_until,state,lock_inherited)
		VALUES($1,$2,$3,$4,$5,now()+interval '30 seconds','starting',true) ON CONFLICT(broadcast_session_id) DO UPDATE SET media_generation=$2,fencing_token=$3,worker_incarnation=$4,run_id=$5,pid=NULL,process_start_ticks=NULL,lease_until=now()+interval '30 seconds',state='starting',lock_inherited=true,updated_at=now()`, s.id, gen, fence, w.incarnation, runID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	r := &running{runID: runID, fence: fence, generation: gen}
	user, pass := workerIdentity(w.Secret, r)
	source, output := paths(s)
	userinfo := url.UserPassword(user, pass).String()
	srcURL := "rtsp://" + userinfo + "@" + w.RTSPHost + "/" + source
	outURL := "rtsp://" + userinfo + "@" + w.RTSPHost + "/" + output
	cmd := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-rtsp_transport", "tcp", "-i", srcURL,
		"-map", "0:v:0", "-map", "0:a:0?", "-c:v", "libx264", "-preset", "veryfast", "-tune", "zerolatency", "-profile:v", "main", "-pix_fmt", "yuv420p",
		"-vf", "scale='min(1280,iw)':'min(720,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2", "-r", "30", "-g", "60", "-keyint_min", "60", "-sc_threshold", "0",
		"-b:v", "2500k", "-maxrate", "2700k", "-bufsize", "500k", "-c:a", "aac", "-b:a", "128k", "-ar", "48000", "-ac", "2", "-f", "rtsp", "-rtsp_transport", "tcp", outURL)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	// Keep the Linux flock open in FFmpeg (fd 3). If the worker dies but FFmpeg
	// survives, a replacement cannot obtain the exclusive lock and start work.
	cmd.ExtraFiles = []*os.File{w.lockFile}
	process, err := Start(cmd)
	if err != nil {
		if process != nil {
			r.process = process
			w.runs[s.id] = r
			w.lostLease[s.id] = true
		} else {
			_, _ = w.DB.Exec(ctx, `UPDATE media_worker_runs SET state='exited',lease_until=now() WHERE run_id=$1`, runID)
		}
		return nil, err
	}
	r.process = process
	w.runs[s.id] = r
	result, err := w.DB.Exec(ctx, `UPDATE media_worker_runs SET pid=$2,process_start_ticks=$3,state='running',updated_at=now() WHERE run_id=$1 AND fencing_token=$4 AND state='starting'`, runID, process.PID, process.StartTicks, fence)
	if err != nil || result.RowsAffected() != 1 {
		term, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		termErr := process.Terminate(term)
		cancel()
		if termErr == nil && !process.GroupAlive() {
			delete(w.runs, s.id)
		} else {
			w.lostLease[s.id] = true
		}
		return nil, fmt.Errorf("cannot record worker process identity")
	}
	return r, nil
}

// transition is the only worker state writer. It uses the same lock order as
// public stop and auth revocation; the outbox shares the transaction.
func (w *Worker) transition(ctx context.Context, s sessionRow, next, reason string, requiredFence int64) error {
	tx, err := w.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var family string
	if err := tx.QueryRow(ctx, `SELECT publisher_auth_family_id::text FROM broadcast_sessions WHERE id=$1`, s.id).Scan(&family); err != nil {
		return err
	}
	var id string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM auth_session_families WHERE id=$1 FOR UPDATE`, family).Scan(&id); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT id::text FROM broadcasts WHERE id=$1 FOR UPDATE`, s.bid).Scan(&id); err != nil {
		return err
	}
	var state string
	var version, fence int64
	if err := tx.QueryRow(ctx, `SELECT state,version,media_fencing_token FROM broadcast_sessions WHERE id=$1 FOR UPDATE`, s.id).Scan(&state, &version, &fence); err != nil {
		return err
	}
	if requiredFence > 0 && fence != requiredFence {
		return nil
	}
	if next == "live" && state != "starting" {
		return nil
	}
	if next == "stopping" && state != "starting" && state != "live" {
		return nil
	}
	if next == "ended" && state != "stopping" {
		return nil
	}
	var ended any
	if next == "ended" {
		ended = time.Now()
	}
	_, err = tx.Exec(ctx, `UPDATE broadcast_sessions SET state=$2,version=version+1,end_reason=COALESCE(NULLIF($3,''),end_reason),ended_at=COALESCE($4,ended_at) WHERE id=$1`, s.id, next, reason, ended)
	if err != nil {
		return err
	}
	if next == "stopping" {
		if _, err = tx.Exec(ctx, `UPDATE publish_grants SET revoked_at=COALESCE(revoked_at,now()) WHERE broadcast_session_id=$1`, s.id); err != nil {
			return err
		}
	}
	if err := db.RecordSessionEvent(ctx, tx, s.bid, s.id, next, version+1); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (w *Worker) cleanup(ctx context.Context, s sessionRow, snap MediaSnapshot) error {
	source, output := paths(s)
	var cleanupErrors []error
	// Stop ingress first so no further media enters while FFmpeg and its
	// outputs are being reclaimed. A failure here must not skip other cleanup.
	for _, conn := range snap.Ingress {
		if conn.Path == source {
			if err := w.MTX.Kick(ctx, "webrtcsessions", conn.ID); err != nil {
				cleanupErrors = append(cleanupErrors, err)
			}
		}
	}
	if run := w.runs[s.id]; run != nil {
		term, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		err := run.process.Terminate(term)
		cancel()
		if err != nil {
			cleanupErrors = append(cleanupErrors, err)
		} else if run.process.GroupAlive() {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("FFmpeg group still alive after termination"))
		} else {
			result, err := w.DB.Exec(ctx, `UPDATE media_worker_runs SET state='exited',lease_until=now(),updated_at=now()
				WHERE broadcast_session_id=$1 AND run_id=$2 AND fencing_token=$3 AND state IN ('starting','running','stopping')`, s.id, run.runID, run.fence)
			if err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("cannot record verified FFmpeg exit: %w", err))
			} else if result.RowsAffected() != 1 {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("verified FFmpeg exit lost its fencing token"))
			} else {
				delete(w.runs, s.id)
			}
		}
	}
	for _, conn := range sessionRTSP(snap, source, output) {
		if err := w.MTX.Kick(ctx, "rtspsessions", conn.ID); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	// Capture closure candidates before the final media snapshot. An Attach
	// that arrives after this point cannot be closed using old absence evidence.
	rows, err := w.DB.Query(ctx, `SELECT id::text,COALESCE(upstream_resource_path,''),request_started_at IS NULL
		FROM media_create_operations WHERE broadcast_session_id=$1 AND state<>'closed'`, s.id)
	if err != nil {
		return errors.Join(append(cleanupErrors, err)...)
	}
	type candidate struct {
		id, path  string
		neverSent bool
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.path, &c.neverSent); err != nil {
			rows.Close()
			return errors.Join(append(cleanupErrors, err)...)
		}
		if c.path != "" || c.neverSent {
			candidates = append(candidates, c)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return errors.Join(append(cleanupErrors, err)...)
	}
	for _, c := range candidates {
		if c.path == "" {
			continue
		}
		user := "create." + c.id
		if err := w.MTX.DeleteWHIP(ctx, c.path, source, user, credential(w.Secret, "create", c.id)); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	if len(cleanupErrors) != 0 {
		return errors.Join(cleanupErrors...)
	}
	check, err := w.MTX.Snapshot(ctx)
	if err != nil {
		return err
	}
	if check.Paths[source].Ready || check.Paths[output].Ready {
		delete(w.absentSince, s.id)
		return nil
	}
	for _, conn := range check.Ingress {
		if conn.Path == source {
			delete(w.absentSince, s.id)
			return nil
		}
	}
	if len(sessionRTSP(check, source, output)) != 0 {
		delete(w.absentSince, s.id)
		return nil
	}
	if first, ok := w.absentSince[s.id]; !ok {
		w.absentSince[s.id] = time.Now()
		return nil
	} else if time.Since(first) < 5*time.Second {
		return nil
	}
	// The same worker holds the exclusive cross-container lock. If an old run
	// still has an unexpired lease, its absence is not yet established.
	var lease time.Time
	var runState string
	var inherited bool
	err = w.DB.QueryRow(ctx, `SELECT state,lease_until,lock_inherited FROM media_worker_runs WHERE broadcast_session_id=$1`, s.id).Scan(&runState, &lease, &inherited)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil && runState != "exited" && (!inherited || time.Now().Before(lease)) {
		return nil
	}
	_, err = w.DB.Exec(ctx, `UPDATE media_worker_runs SET state='exited',lease_until=now(),updated_at=now() WHERE broadcast_session_id=$1`, s.id)
	if err != nil {
		return err
	}
	// An unknown POST has no proven server-side lifetime bound. It remains
	// unresolved and keeps the session STOPPING until a resource is recorded or
	// an operator reconciles it with separate evidence.
	for _, c := range candidates {
		if c.path != "" {
			_, err = w.DB.Exec(ctx, `UPDATE media_create_operations SET state='closed',resolved_at=now()
				WHERE id=$1 AND broadcast_session_id=$2 AND upstream_resource_path=$3 AND state<>'closed'`, c.id, s.id, c.path)
		} else if c.neverSent {
			_, err = w.DB.Exec(ctx, `UPDATE media_create_operations SET state='closed',resolved_at=now()
				WHERE id=$1 AND broadcast_session_id=$2 AND upstream_resource_path IS NULL AND request_started_at IS NULL AND state<>'closed'`, c.id, s.id)
		}
		if err != nil {
			return err
		}
	}
	var unresolved bool
	if err := w.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_create_operations WHERE broadcast_session_id=$1 AND state<>'closed')`, s.id).Scan(&unresolved); err != nil {
		return err
	}
	if unresolved {
		return nil
	}
	return w.transition(ctx, s, "ended", "", 0)
}
