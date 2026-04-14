package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type Status struct {
	Running                bool   `json:"running"`
	RestartRequired        bool   `json:"restart_required"`
	ConfigPath             string `json:"config_path"`
	SubscriptionConfigured bool   `json:"subscription_configured"`
	LastError              string `json:"last_error,omitempty"`
	LastApplyAt            string `json:"last_apply_at,omitempty"`
	LastApplyStatus        string `json:"last_apply_status,omitempty"`
	PID                    int    `json:"pid,omitempty"`
}

type Process struct {
	Binary string
	Config string

	mu              sync.RWMutex
	cmd             *exec.Cmd
	running         bool
	restartRequired bool
	lastError       string
	lastApplyAt     string
	lastApplyStatus string
}

func (p *Process) Check(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, p.Binary, "check", "-c", p.Config)
	output, err := cmd.CombinedOutput()
	if err != nil {
		p.setLastError(fmt.Sprintf("sing-box check failed: %v: %s", err, string(output)))
		return err
	}
	p.setLastError("")
	return nil
}

func (p *Process) WriteConfig(content string) error {
	return os.WriteFile(p.Config, []byte(content), 0o644)
}

func (p *Process) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		return nil
	}
	cmd := exec.Command(p.Binary, "run", "-c", p.Config)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		p.lastError = err.Error()
		return err
	}
	p.cmd = cmd
	p.running = true
	p.lastError = ""
	go p.wait(cmd)
	return nil
}

func (p *Process) wait(cmd *exec.Cmd) {
	err := cmd.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == cmd {
		p.cmd = nil
	}
	p.running = false
	if err != nil {
		p.lastError = err.Error()
	}
}

func (p *Process) Stop(ctx context.Context) error {
	p.mu.RLock()
	cmd := p.cmd
	p.mu.RUnlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return ctx.Err()
	case <-done:
		p.mu.Lock()
		p.cmd = nil
		p.running = false
		p.mu.Unlock()
		return nil
	}
}

func (p *Process) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}

func (p *Process) Snapshot(subscriptionConfigured bool) Status {
	p.mu.RLock()
	defer p.mu.RUnlock()
	status := Status{
		Running:                p.running,
		RestartRequired:        p.restartRequired,
		ConfigPath:             p.Config,
		SubscriptionConfigured: subscriptionConfigured,
		LastError:              p.lastError,
		LastApplyAt:            p.lastApplyAt,
		LastApplyStatus:        p.lastApplyStatus,
	}
	if p.cmd != nil && p.cmd.Process != nil {
		status.PID = p.cmd.Process.Pid
	}
	return status
}

func (p *Process) RecordApplyResult(status string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastApplyStatus = status
	p.lastApplyAt = time.Now().UTC().Format(time.RFC3339)
}

func (p *Process) setLastError(msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastError = msg
}
