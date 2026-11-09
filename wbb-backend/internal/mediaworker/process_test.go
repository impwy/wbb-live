package mediaworker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestTerminateKillsVerifiedProcessGroup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux process namespace is verified in the worker container")
	}
	cmd := exec.Command("/bin/sh", "-c", "sleep 30 & wait")
	run, err := Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if run.PID < 1 || run.StartTicks < 1 {
		t.Fatalf("identity: %+v", run)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := run.Terminate(ctx); err != nil {
		t.Fatal(err)
	}
	if run.GroupAlive() {
		t.Fatal("grandchild survived group termination")
	}
}

func TestWorkerLockSurvivesParentFDClosureWhileChildLives(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux flock inheritance is verified in the worker container")
	}
	name := filepath.Join(t.TempDir(), "worker.lock")
	file, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sleep", "30")
	cmd.ExtraFiles = []*os.File{file}
	run, err := Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	probe, err := os.OpenFile(name, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		t.Fatal("replacement worker acquired lock while child lived")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := run.Terminate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("replacement cannot acquire after child exit: %v", err)
	}
}

func TestTerminateEscalatesIgnoringTermAndRejectsReusedPID(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux process namespace is verified in the worker container")
	}
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command("/bin/sh", "-c", "trap '' TERM; printf ready > \"$1\"; while :; do sleep 1; done", "sh", ready)
	run, err := Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("TERM ignore handshake missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	realTicks := run.StartTicks
	run.StartTicks++
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := run.Terminate(ctx); err == nil || !run.GroupAlive() {
		t.Fatalf("reused PID guard: err=%v alive=%v", err, run.GroupAlive())
	}
	run.StartTicks = realTicks
	started := time.Now()
	if err := run.Terminate(ctx); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) < 900*time.Millisecond {
		t.Fatal("SIGKILL escalation was not exercised")
	}
	if run.GroupAlive() {
		t.Fatal("SIGTERM-ignoring descendant survived SIGKILL")
	}
}
