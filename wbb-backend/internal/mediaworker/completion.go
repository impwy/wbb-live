package mediaworker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/impwy/wbb-live/wbb-backend/internal/mediacompletion"
	"github.com/jackc/pgx/v5"
)

type completionOperation struct {
	id, path, sourcePath string
	legacy, neverSent    bool
	tuple                mediacompletion.Tuple
}

func (w *Worker) pendingOperations(ctx context.Context, sid string) ([]completionOperation, error) {
	rows, err := w.DB.Query(ctx, `SELECT id::text,COALESCE(upstream_resource_path,''),COALESCE(completion_source_path,''),completion_legacy,
		request_started_at IS NULL,COALESCE(completion_contract_version,''),COALESCE(completion_root,''),
		COALESCE(completion_slot,''),COALESCE(completion_epoch,''),COALESCE(completion_epoch_sequence,0)
		FROM media_create_operations WHERE broadcast_session_id=$1 AND state<>'closed'`, sid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var operations []completionOperation
	for rows.Next() {
		var op completionOperation
		if err := rows.Scan(&op.id, &op.path, &op.sourcePath, &op.legacy, &op.neverSent, &op.tuple.ContractVersion,
			&op.tuple.Root, &op.tuple.Slot, &op.tuple.Epoch, &op.tuple.Sequence); err != nil {
			return nil, err
		}
		operations = append(operations, op)
	}
	return operations, rows.Err()
}

// recordCompletion is the sole v1 closed writer. It shares the family ->
// broadcast -> session lock order with Stop and MarkDispatched. Historical
// operations are never assigned a guessed epoch or a never-sent proof.
func (w *Worker) recordCompletion(ctx context.Context, s sessionRow, op completionOperation, evidence *mediacompletion.Evidence) error {
	tx, err := w.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var family, locked, state string
	if err = tx.QueryRow(ctx, `SELECT publisher_auth_family_id::text FROM broadcast_sessions WHERE id=$1`, s.id).Scan(&family); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT id::text FROM auth_session_families WHERE id=$1 FOR UPDATE`, family).Scan(&locked); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT id::text FROM broadcasts WHERE id=$1 FOR UPDATE`, s.bid).Scan(&locked); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT state FROM broadcast_sessions WHERE id=$1 AND media_generation=$2 FOR UPDATE`, s.id, s.generation).Scan(&state); err != nil {
		return err
	}
	if state != "stopping" {
		return fmt.Errorf("media session is no longer stopping")
	}
	var resultRows int64
	if evidence == nil {
		result, err := tx.Exec(ctx, `UPDATE media_create_operations SET state='closed',resolved_at=now(),completion_evidence_kind='never_sent'
			WHERE id=$1 AND broadcast_session_id=$2 AND media_generation=$3 AND state<>'closed' AND NOT completion_legacy
			AND request_started_at IS NULL AND completion_epoch IS NULL`, op.id, s.id, s.generation)
		if err != nil {
			return err
		}
		resultRows = result.RowsAffected()
	} else {
		if !evidence.Completed() || evidence.Operation != op.id || evidence.Tuple != op.tuple {
			return fmt.Errorf("unverified completion evidence")
		}
		result, err := tx.Exec(ctx, `UPDATE media_create_operations SET state='closed',resolved_at=now(),
			completion_evidence_kind='completed',completion_confirmed_at=now(),completion_revision=$4
			WHERE id=$1 AND broadcast_session_id=$2 AND media_generation=$3 AND state<>'closed' AND NOT completion_legacy
			AND completion_contract_version=$5 AND completion_root=$6 AND completion_slot=$7
			AND completion_epoch=$8 AND completion_epoch_sequence=$9`, op.id, s.id, s.generation,
			evidence.Revision, op.tuple.ContractVersion, op.tuple.Root, op.tuple.Slot, op.tuple.Epoch, op.tuple.Sequence)
		if err != nil {
			return err
		}
		resultRows = result.RowsAffected()
	}
	if resultRows != 1 {
		return fmt.Errorf("completion ownership changed")
	}
	return tx.Commit(ctx)
}

func (w *Worker) closeOperations(ctx context.Context, s sessionRow, operations []completionOperation) error {
	var failures []error
	wantSource, _ := paths(s)
	for _, op := range operations {
		if op.legacy {
			continue
		}
		if op.neverSent && op.tuple.Epoch == "" {
			if err := w.recordCompletion(ctx, s, op, nil); err != nil {
				failures = append(failures, err)
			}
			continue
		}
		if op.tuple.Epoch == "" || w.Completion == nil {
			failures = append(failures, fmt.Errorf("completion ownership unavailable for operation %s", op.id))
			continue
		}
		if op.sourcePath != wantSource {
			failures = append(failures, fmt.Errorf("completion source path does not match session %s", s.id))
			continue
		}
		// A lost close response is recoverable by querying the pinned tuple. A
		// non-completed query leaves this operation unresolved for the next tick.
		_, closeErr := w.Completion.Close(ctx, op.tuple, op.id)
		evidence, queryErr := w.Completion.Query(ctx, op.tuple, op.id)
		if queryErr != nil || !evidence.Completed() {
			failures = append(failures, errors.Join(closeErr, queryErr, fmt.Errorf("operation %s is not completed", op.id)))
			continue
		}
		if err := w.recordCompletion(ctx, s, op, &evidence); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (w *Worker) completionReady(ctx context.Context, tx pgx.Tx, s sessionRow) (bool, error) {
	var unresolved bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_create_operations
		WHERE broadcast_session_id=$1 AND (completion_legacy OR state<>'closed' OR completion_evidence_kind IS NULL))`, s.id).Scan(&unresolved)
	if err != nil {
		return false, err
	}
	if unresolved {
		return false, nil
	}
	var activeRun bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_worker_runs WHERE broadcast_session_id=$1
		AND (state<>'exited' OR media_generation<>$2 OR fencing_token<>$3))`, s.id, s.generation, s.fence).Scan(&activeRun)
	return !activeRun, err
}

func (w *Worker) confirmRecoveredRunExit(ctx context.Context, s sessionRow) error {
	if w.lockFile == nil {
		return errors.New("worker process lock is unavailable")
	}
	var runID, incarnation, state string
	var generation, fence int64
	var lease time.Time
	var inherited bool
	err := w.DB.QueryRow(ctx, `SELECT run_id::text,worker_incarnation::text,media_generation,fencing_token,state,lease_until,lock_inherited
		FROM media_worker_runs WHERE broadcast_session_id=$1`, s.id).Scan(&runID, &incarnation, &generation, &fence, &state, &lease, &inherited)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if state == "exited" {
		return nil
	}
	if incarnation == w.incarnation || !inherited || time.Now().Before(lease) || generation != s.generation || fence != s.fence {
		return nil
	}
	// Acquiring the fixed-inode lock at Run start proves that no old FFmpeg
	// holding the inherited open-file description survives. The update is
	// fenced by the exact durable execution identity observed above.
	result, err := w.DB.Exec(ctx, `UPDATE media_worker_runs SET state='exited',lease_until=now(),updated_at=now()
		WHERE broadcast_session_id=$1 AND run_id=$2 AND worker_incarnation=$3
		AND media_generation=$4 AND fencing_token=$5 AND lock_inherited AND lease_until<=now()
		AND state IN ('starting','running','stopping')`, s.id, runID, incarnation, generation, fence)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errors.New("recovered worker identity changed")
	}
	return nil
}

func (w *Worker) recordVerifiedRunExit(ctx context.Context, sid string, run *running) error {
	if run == nil || run.process == nil || run.process.GroupAlive() {
		return errors.New("FFmpeg process group exit is unverified")
	}
	result, err := w.DB.Exec(ctx, `UPDATE media_worker_runs SET state='exited',lease_until=now(),updated_at=now()
		WHERE broadcast_session_id=$1 AND run_id=$2 AND fencing_token=$3 AND media_generation=$4
		AND worker_incarnation=$5 AND state IN ('starting','running','stopping')`,
		sid, run.runID, run.fence, run.generation, w.incarnation)
	if err != nil {
		return fmt.Errorf("cannot record verified FFmpeg exit: %w", err)
	}
	if result.RowsAffected() != 1 {
		// The same run may be retried after its exit was committed but the
		// session transition failed. Accept only that exact durable identity.
		var state string
		if err := w.DB.QueryRow(ctx, `SELECT state FROM media_worker_runs WHERE broadcast_session_id=$1
			AND run_id=$2 AND fencing_token=$3 AND media_generation=$4 AND worker_incarnation=$5`,
			sid, run.runID, run.fence, run.generation, w.incarnation).Scan(&state); err != nil || state != "exited" {
			return errors.New("verified FFmpeg exit lost its execution identity")
		}
	}
	return nil
}
