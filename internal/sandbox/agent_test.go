package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWrapAgentDockerArgs: the agent runs inside docker — absolute bind mount,
// egress network (NOT none, NOT open bridge), container env injected as -e, and
// the agent argv at the tail.
func TestWrapAgentDockerArgs(t *testing.T) {
	d := &DockerSandbox{cfg: Config{Workdir: ".vibeforge-runs/run_x.agent", Image: "claude-img", Network: "vibeforge-egress"}}
	argv := []string{"claude", "-p", "--model", "claude-opus-4-8", "do the thing"}
	hostArgv, hostEnv := d.WrapAgent(argv, []string{"HTTPS_PROXY=http://egress-proxy:8888", "ANTHROPIC_API_KEY=sentinel"})

	if hostArgv[0] != "docker" || hostArgv[1] != "run" {
		t.Fatalf("expected `docker run`, got %v", hostArgv[:2])
	}
	// absolute bind mount
	var mount, network string
	for i, a := range hostArgv {
		if a == "-v" && i+1 < len(hostArgv) {
			mount = hostArgv[i+1]
		}
		if a == "--network" && i+1 < len(hostArgv) {
			network = hostArgv[i+1]
		}
	}
	src := mount[:len(mount)-len(":/work")]
	if !filepath.IsAbs(src) {
		t.Fatalf("agent bind mount must be absolute, got %q", mount)
	}
	// egress network, NOT none and NOT the open default bridge
	if network != "vibeforge-egress" {
		t.Fatalf("agent must use the egress network, got %q", network)
	}
	if network == "none" || network == "bridge" {
		t.Fatalf("agent network must not be none/bridge (open), got %q", network)
	}
	// container env injected as -e
	if !hasPair(hostArgv, "-e", "HTTPS_PROXY=http://egress-proxy:8888") {
		t.Fatalf("expected HTTPS_PROXY injected as -e, got %v", hostArgv)
	}
	// the agent argv is at the tail (after the image)
	if hostArgv[len(hostArgv)-1] != "do the thing" || hostArgv[len(hostArgv)-5] != "claude" {
		t.Fatalf("agent argv should be the tail, got %v", hostArgv[len(hostArgv)-6:])
	}
	// host env for the `docker` binary carries no daemon secrets
	for _, kv := range hostEnv {
		if kv == "GH_TOKEN=x" {
			t.Fatalf("docker cli host env must not carry secrets")
		}
	}
}

// TestAgentNetworkDiffersFromGate: the agent gets an egress-allowlist network;
// the gate gets --network none. Different policies, by design.
func TestAgentNetworkDiffersFromGate(t *testing.T) {
	cfg := Config{Workdir: t.TempDir()}

	// gate: EgressDeny -> --network none
	gateCfg := cfg
	gateCfg.EgressDeny = true
	gateArgs, err := dockerArgs(gateCfg, "true", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !hasPair(gateArgs, "--network", "none") {
		t.Fatalf("gate must use --network none, got %v", gateArgs)
	}

	// agent: egress network (named), never none
	agentCfg := cfg
	agentCfg.Network = "vibeforge-egress"
	d := &DockerSandbox{cfg: agentCfg}
	agentArgv, _ := d.WrapAgent([]string{"claude", "-p"}, nil)
	if hasPair(agentArgv, "--network", "none") {
		t.Fatalf("agent must NOT use --network none")
	}
	if !hasPair(agentArgv, "--network", "vibeforge-egress") {
		t.Fatalf("agent must use the egress network, got %v", agentArgv)
	}
}

// TestLocalWrapAgentRunsOnHost: the local fallback returns the argv unchanged and
// the given (scrubbed) env — runs on the host, not in docker.
func TestLocalWrapAgentRunsOnHost(t *testing.T) {
	l := &LocalSandbox{cfg: Config{Workdir: t.TempDir()}}
	argv := []string{"claude", "-p", "hi"}
	host, env := l.WrapAgent(argv, []string{"PATH=/usr/bin", "ANTHROPIC_API_KEY=k"})
	if host[0] != "claude" {
		t.Fatalf("local must run the argv directly, got %v", host)
	}
	if len(env) != 2 {
		t.Fatalf("local must pass the env through, got %v", env)
	}
}

// TestSyncBackPropagatesAndKeepsGit: edits in the agent's .git-less tree land in
// the run worktree, and the run worktree's .git is preserved. Deletes propagate.
func TestSyncBackPropagatesAndKeepsGit(t *testing.T) {
	run := filepath.Join(t.TempDir(), "run")
	mustWrite(t, filepath.Join(run, ".git", "HEAD"), "ref: x")
	mustWrite(t, filepath.Join(run, "old.txt"), "remove me")
	mustWrite(t, filepath.Join(run, ".vibeforge-gate"), "true")

	agent := run + ".agent"
	if err := CopyTreeNoGit(run, agent); err != nil {
		t.Fatal(err)
	}
	// agent edits: add a file, delete old.txt
	mustWrite(t, filepath.Join(agent, "new.txt"), "agent made this")
	if err := os.Remove(filepath.Join(agent, "old.txt")); err != nil {
		t.Fatal(err)
	}

	if err := SyncBack(agent, run); err != nil {
		t.Fatal(err)
	}
	// new file present in run worktree
	if !exists(filepath.Join(run, "new.txt")) {
		t.Fatal("agent's new file did not propagate to the run worktree")
	}
	// deleted file is gone
	if exists(filepath.Join(run, "old.txt")) {
		t.Fatal("delete did not propagate (old.txt still present)")
	}
	// .git preserved
	if !exists(filepath.Join(run, ".git", "HEAD")) {
		t.Fatal(".git must be preserved in the run worktree")
	}
	// agent never saw .git
	if exists(filepath.Join(agent, ".git")) {
		t.Fatal("agent worktree must not contain .git")
	}
}

func hasPair(args []string, flag, val string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == val {
			return true
		}
	}
	return false
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
