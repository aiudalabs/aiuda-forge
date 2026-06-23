package sandbox

import (
	"os"
	"path/filepath"
)

// dockerCliKeep is the env the HOST `docker` binary may see (system + docker
// client config). Crucially it is NOT the container env — daemon secrets
// (GH_TOKEN, DB_*, the real LLM key) are absent, so nothing crosses the border
// via the docker client. Port of v1's docker_cli_env().
var dockerCliKeep = []string{
	"PATH", "HOME", "LANG", "LC_ALL", "LC_CTYPE", "TZ", "TERM", "TMPDIR", "USER", "SHELL",
	"DOCKER_HOST", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "DOCKER_CONFIG",
}

// dockerCliEnv returns the env for invoking the host `docker` binary.
func dockerCliEnv() []string { return FilterEnv(os.Environ(), dockerCliKeep) }

// agentNetwork resolves the egress network for the agent. It must never be
// "none" (the agent needs the LLM API) and never the open default "bridge".
func (c Config) agentNetwork() string {
	if c.Network != "" {
		return c.Network
	}
	return DefaultEgressNetwork
}

// WrapAgent (DockerSandbox) wraps the agent argv to run inside a per-task
// container: `docker run --rm --network <egress> -v <abs>:/work -w /work
// -e <egress/auth allowlist> <image> <argv...>`. The container env (containerEnv)
// is injected as -e flags; the HOST process that runs `docker` gets only
// dockerCliEnv (no secrets). Network is the egress-allowlist net (NOT none).
func (d *DockerSandbox) WrapAgent(argv []string, containerEnv []string) ([]string, []string) {
	image := d.cfg.Image
	if image == "" {
		image = "alpine:3.20" // NOTE: must be an image that contains `claude`
	}
	abs, err := filepath.Abs(d.cfg.Workdir)
	if err != nil {
		abs = d.cfg.Workdir
	}
	hostArgv := []string{"docker", "run", "--rm", "-i"}
	if d.cfg.OCIRuntime != "" {
		hostArgv = append(hostArgv, "--runtime", d.cfg.OCIRuntime)
	}
	// Egress network (allowlist, default-deny) — explicitly NOT --network none.
	hostArgv = append(hostArgv, "--network", d.cfg.agentNetwork())
	if d.cfg.UID != "" {
		hostArgv = append(hostArgv, "--user", d.cfg.UID)
	}
	hostArgv = append(hostArgv, "-v", abs+":/work", "-w", "/work")
	for _, kv := range containerEnv {
		hostArgv = append(hostArgv, "-e", kv)
	}
	hostArgv = append(hostArgv, image)
	hostArgv = append(hostArgv, argv...)
	return hostArgv, dockerCliEnv()
}

// WrapAgent (LocalSandbox) runs the agent argv ON THE HOST (the flagged
// fallback): the argv is unchanged and containerEnv becomes the process env
// (already scrubbed of daemon secrets by the caller). NOT a security boundary.
func (l *LocalSandbox) WrapAgent(argv []string, containerEnv []string) ([]string, []string) {
	return argv, containerEnv
}
