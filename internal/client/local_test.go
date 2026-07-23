package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pantalk/pantalk/internal/config"
)

type fakeEmbeddedLocalServer struct {
	ready      chan struct{}
	runStarted chan struct{}
	stopped    chan struct{}
	startErr   error
}

func newFakeEmbeddedLocalServer(startErr error) *fakeEmbeddedLocalServer {
	return &fakeEmbeddedLocalServer{
		ready:      make(chan struct{}),
		runStarted: make(chan struct{}),
		stopped:    make(chan struct{}),
		startErr:   startErr,
	}
}

func (s *fakeEmbeddedLocalServer) Ready() <-chan struct{} {
	return s.ready
}

func (s *fakeEmbeddedLocalServer) RunContext(ctx context.Context) error {
	close(s.runStarted)
	if s.startErr != nil {
		return s.startErr
	}
	close(s.ready)
	<-ctx.Done()
	close(s.stopped)
	return nil
}

func TestRunLocalModeBuildsCodexConfigurationAndStopsServer(t *testing.T) {
	workdir := t.TempDir()
	stateDir := t.TempDir()
	fakeServer := newFakeEmbeddedLocalServer(nil)
	configured := make(chan config.Config, 1)
	chatCalled := make(chan struct{}, 1)

	err := runLocalMode(localOptions{
		workdir:     workdir,
		user:        "alice",
		driver:      "codex",
		statePath:   filepath.Join(stateDir, "local.db"),
		socketPath:  filepath.Join(stateDir, "local.sock"),
		codexBinary: "/opt/codex",
		model:       "test-model",
		effort:      "high",
		sandbox:     "read-only",
		approval:    "never",
		timeout:     300,
	}, localDependencies{
		newServer: func(cfg config.Config, debug bool) embeddedLocalServer {
			if debug {
				t.Error("debug unexpectedly enabled")
			}
			configured <- cfg
			return fakeServer
		},
		runChat: func(socketPath string, user string) int {
			if socketPath != filepath.Join(stateDir, "local.sock") {
				t.Errorf("unexpected chat socket %q", socketPath)
			}
			if user != "alice" {
				t.Errorf("unexpected chat user %q", user)
			}
			chatCalled <- struct{}{}
			return 0
		},
	})
	if err != nil {
		t.Fatalf("run local mode: %v", err)
	}

	cfg := <-configured
	if len(cfg.Bots) != 1 || cfg.Bots[0].Name != localBotName || cfg.Bots[0].Type != "local" {
		t.Fatalf("unexpected local bot config: %+v", cfg.Bots)
	}
	if len(cfg.Agents) != 1 {
		t.Fatalf("unexpected agents: %+v", cfg.Agents)
	}
	agent := cfg.Agents[0]
	if agent.Name != "local-codex" ||
		agent.Driver != "codex" ||
		agent.Workdir != workdir ||
		agent.Timeout != 300 {
		t.Fatalf("unexpected local agent config: %+v", agent)
	}
	if len(cfg.Bots[0].Agents) != 1 ||
		cfg.Bots[0].Agents[0].Agent != agent.Name ||
		cfg.Bots[0].Agents[0].When != "notify" {
		t.Fatalf("unexpected local agent binding: %+v", cfg.Bots[0].Agents)
	}
	if agent.Codex.Binary != "/opt/codex" ||
		agent.Codex.Model != "test-model" ||
		agent.Codex.Effort != "high" ||
		agent.Codex.Sandbox != "read-only" ||
		agent.Codex.ApprovalPolicy != "never" {
		t.Fatalf("unexpected Codex config: %+v", agent.Codex)
	}
	if !strings.Contains(agent.Instructions, "local Pantalk test conversation") {
		t.Fatalf("default instructions were not applied: %q", agent.Instructions)
	}

	<-chatCalled
	<-fakeServer.stopped
}

func TestRunLocalModeEphemeralStateIsRemoved(t *testing.T) {
	fakeServer := newFakeEmbeddedLocalServer(nil)
	configured := make(chan config.Config, 1)

	err := runLocalMode(localOptions{
		workdir:   t.TempDir(),
		user:      "alice",
		driver:    "codex",
		sandbox:   "read-only",
		approval:  "never",
		timeout:   300,
		ephemeral: true,
	}, localDependencies{
		newServer: func(cfg config.Config, _ bool) embeddedLocalServer {
			configured <- cfg
			return fakeServer
		},
		runChat: func(string, string) int { return 0 },
	})
	if err != nil {
		t.Fatalf("run local mode: %v", err)
	}

	cfg := <-configured
	stateDir := filepath.Dir(cfg.Server.DBPath)
	if _, err := os.Stat(stateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ephemeral directory still exists or returned unexpected error: %v", err)
	}
}

func TestRunLocalModeRejectsUnsupportedDriver(t *testing.T) {
	err := runLocalMode(localOptions{
		workdir:  t.TempDir(),
		user:     "alice",
		driver:   "goose",
		sandbox:  "read-only",
		approval: "never",
		timeout:  300,
	}, localDependencies{})
	if err == nil || !strings.Contains(err.Error(), "use codex or claude") {
		t.Fatalf("expected unsupported driver error, got %v", err)
	}
}

func TestRunLocalModeBuildsClaudeConfigurationAndStopsServer(t *testing.T) {
	workdir := t.TempDir()
	stateDir := t.TempDir()
	fakeServer := newFakeEmbeddedLocalServer(nil)
	configured := make(chan config.Config, 1)

	err := runLocalMode(localOptions{
		workdir:        workdir,
		user:           "alice",
		driver:         "claude",
		statePath:      filepath.Join(stateDir, "local.db"),
		socketPath:     filepath.Join(stateDir, "local.sock"),
		claudeBinary:   "/opt/claude",
		model:          "sonnet",
		effort:         "high",
		permissionMode: "plan",
		timeout:        300,
	}, localDependencies{
		newServer: func(cfg config.Config, debug bool) embeddedLocalServer {
			if debug {
				t.Error("debug unexpectedly enabled")
			}
			configured <- cfg
			return fakeServer
		},
		runChat: func(string, string) int { return 0 },
	})
	if err != nil {
		t.Fatalf("run local mode: %v", err)
	}

	cfg := <-configured
	if len(cfg.Agents) != 1 {
		t.Fatalf("unexpected agents: %+v", cfg.Agents)
	}
	agent := cfg.Agents[0]
	if agent.Name != "local-claude" ||
		agent.Driver != "claude" ||
		agent.Workdir != workdir ||
		agent.Timeout != 300 {
		t.Fatalf("unexpected local Claude agent config: %+v", agent)
	}
	if agent.Claude.Binary != "/opt/claude" ||
		agent.Claude.Model != "sonnet" ||
		agent.Claude.Effort != "high" ||
		agent.Claude.PermissionMode != "plan" {
		t.Fatalf("unexpected Claude config: %+v", agent.Claude)
	}

	<-fakeServer.stopped
}

func TestRunLocalModeRejectsUnsupportedClaudePermissionMode(t *testing.T) {
	err := runLocalMode(localOptions{
		workdir:        t.TempDir(),
		user:           "alice",
		driver:         "claude",
		permissionMode: "unrestricted",
		timeout:        300,
	}, localDependencies{})
	if err == nil || !strings.Contains(err.Error(), "unsupported --permission-mode") {
		t.Fatalf("expected unsupported permission mode error, got %v", err)
	}
}

func TestRunLocalHelpSucceeds(t *testing.T) {
	if code := runLocal([]string{"--help"}); code != 0 {
		t.Fatalf("help exit code = %d, want 0", code)
	}
}

func TestRunLocalModeRejectsExplicitStateWithEphemeral(t *testing.T) {
	err := runLocalMode(localOptions{
		workdir:       t.TempDir(),
		user:          "alice",
		driver:        "codex",
		sandbox:       "read-only",
		approval:      "never",
		timeout:       300,
		ephemeral:     true,
		stateExplicit: true,
	}, localDependencies{})
	if err == nil || !strings.Contains(err.Error(), "--ephemeral cannot be combined") {
		t.Fatalf("expected conflicting state error, got %v", err)
	}
}

func TestRunLocalModeReportsServerStartupFailure(t *testing.T) {
	stateDir := t.TempDir()
	fakeServer := newFakeEmbeddedLocalServer(errors.New("codex unavailable"))
	chatStarted := make(chan struct{}, 1)

	err := runLocalMode(localOptions{
		workdir:    t.TempDir(),
		user:       "alice",
		driver:     "codex",
		statePath:  filepath.Join(stateDir, "local.db"),
		socketPath: filepath.Join(stateDir, "local.sock"),
		sandbox:    "read-only",
		approval:   "never",
		timeout:    300,
	}, localDependencies{
		newServer: func(config.Config, bool) embeddedLocalServer {
			return fakeServer
		},
		runChat: func(string, string) int {
			chatStarted <- struct{}{}
			return 1
		},
	})
	if err == nil || !strings.Contains(err.Error(), "codex unavailable") {
		t.Fatalf("expected startup failure, got %v", err)
	}
	select {
	case <-chatStarted:
		t.Fatal("chat started after a server startup failure")
	default:
	}
}
