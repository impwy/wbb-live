package mediaworker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Process is an FFmpeg execution identity inside the worker PID namespace.
// PID is never used alone: /proc start ticks reject a reused PID.
type Process struct {
	PID        int
	StartTicks uint64
	cmd        *exec.Cmd
	done       chan error
}

func Start(cmd *exec.Cmd) (*Process, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &Process{PID: cmd.Process.Pid, cmd: cmd, done: make(chan error, 1)}
	go func() { p.done <- cmd.Wait() }()
	for attempt := 0; attempt < 10; attempt++ {
		if ticks, err := procStartTicks(p.PID); err == nil {
			p.StartTicks = ticks
			return p, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = syscall.Kill(-p.PID, syscall.SIGKILL)
	deadline := time.Now().Add(2 * time.Second)
	for p.GroupAlive() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if p.GroupAlive() {
		return p, fmt.Errorf("cannot identify or reap child process group")
	}
	return nil, fmt.Errorf("cannot identify child process start time")
}

func procStartTicks(pid int) (uint64, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	close := strings.LastIndexByte(string(raw), ')')
	if close < 0 {
		return 0, errors.New("bad proc stat")
	}
	fields := strings.Fields(string(raw[close+1:]))
	if len(fields) < 20 {
		return 0, errors.New("short proc stat")
	}
	return strconv.ParseUint(fields[19], 10, 64)
}

func (p *Process) GroupAlive() bool {
	err := syscall.Kill(-p.PID, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func (p *Process) Terminate(ctx context.Context) error {
	ticks, err := procStartTicks(p.PID)
	if err == nil && ticks != p.StartTicks {
		return fmt.Errorf("PID %d reused", p.PID)
	}
	// ENOENT is safe only while this same Process object still owns a live
	// process group: Linux cannot reuse its PGID until the last member exits.
	// Any other /proc failure is unknown identity and must not signal a group.
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot verify process identity: %w", err)
	}
	if !p.GroupAlive() {
		return nil
	}
	_ = syscall.Kill(-p.PID, syscall.SIGTERM)
	grace := time.NewTimer(time.Second)
	defer grace.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			if p.GroupAlive() {
				_ = syscall.Kill(-p.PID, syscall.SIGKILL)
			}
			return ctx.Err()
		case <-grace.C:
			if p.GroupAlive() {
				_ = syscall.Kill(-p.PID, syscall.SIGKILL)
			}
		case <-tick.C:
			if !p.GroupAlive() {
				return nil
			}
		}
	}
}
