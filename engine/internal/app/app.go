// Package app assembles the kernel: store + executor engine + all step runners +
// the selected agent backend + event bus + HTTP API. Both cmd/control and the
// contract tests build through here, so the wiring is exercised exactly once and
// there is no separate test-only path.
package app

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"forge/internal/agent"
	"forge/internal/api"
	"forge/internal/auth"
	"forge/internal/billing"
	"forge/internal/brain"
	"forge/internal/channels"
	"forge/internal/channels/telegram"
	"forge/internal/conductor"
	"forge/internal/cron"
	"forge/internal/digest"
	"forge/internal/gate"
	"forge/internal/github"
	"forge/internal/pr"
	"forge/internal/projects"
	"forge/internal/release"
	"forge/internal/sandbox"
	"forge/internal/store"
	"forge/internal/tickets"
	"forge/internal/workflow"
)

// Config configures an assembled kernel.
type Config struct {
	DBPath         string        // sqlite path
	TicketsDB      string        // tickets sqlite path; "" disables the ticket store
	ProjectsDB     string        // projects sqlite path; "" disables the project store
	AuthDB         string        // auth sqlite path; "" disables local auth
	RegistryRoot   string        // registry/
	WorkdirRoot    string        // where per-run working trees live
	EngineMode     string        // "echo" (FakeBackend) | "claude" (real)
	SandboxRuntime string        // "", "docker", "local"; "runsc" via env
	Backend        agent.Backend // optional override (tests inject a fake)
	AgentAuth      agent.Auth    // auth for the real backend
	AgentTimeout   time.Duration // per-agent wall clock
	Workers        int           // in-process worker pool size (<=0 → 1)
}

// App is the assembled kernel.
type App struct {
	approver *conductor.Approver
	Store    *store.Store
	Tickets  *tickets.Store  // nil when TicketsDB is not configured
	Projects *projects.Store // nil when ProjectsDB is not configured
	Auth     *auth.Store     // nil when AuthDB is not configured
	Engine   *workflow.Engine
	Bus      *api.Bus
	Server   *api.Server
	workers  int
	// PreviewSecret is the HMAC key that mints/verifies preview capability tokens.
	// cmd/control wires it into the auth middleware so /previews is authorized ONLY
	// by preview-scoped tokens (never a session/service token).
	PreviewSecret []byte

	// Daily digest scheduler (standup push). digester + channels + the parsed cron
	// are wired in Build; the loop runs from StartBackground when all three exist.
	digester       *digest.Digester
	digestChannels channels.Registry
	digestCron     cron.Schedule
}

// Build assembles a kernel per cfg.
func Build(cfg Config) (*App, error) {
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}

	wfLoader := workflow.NewDirLoader(cfg.RegistryRoot + "/workflows")
	agentLoader := agent.NewDirLoader(cfg.RegistryRoot + "/agents")

	eng := workflow.NewEngine(st, wfLoader, cfg.WorkdirRoot)

	// Step runners — registered by type, the only place runners are wired.
	eng.Register("echo", workflow.EchoRunner{})

	// requireDocker: when set, the agent and gate hard-fail (no LocalSandbox
	// fallback) if real docker isolation is unavailable (audit C2/C2b). It is ON
	// whenever the operator asked for docker (VIBEFORGE_SANDBOX=docker) or set
	// VIBEFORGE_REQUIRE_SANDBOX=1, UNLESS explicitly opted out for dev/test with
	// VIBEFORGE_ALLOW_LOCAL_SANDBOX=1. Untrusted repo code must never run on the host.
	requireDocker := dockerRequired(cfg.SandboxRuntime)

	hardGate := gate.NewHardenedRunner()
	hardGate.SandboxTemplate = sandbox.Config{
		Runtime:       cfg.SandboxRuntime,
		OCIRuntime:    os.Getenv("VIBEFORGE_SANDBOX_RUNTIME"),
		Image:         os.Getenv("VIBEFORGE_SANDBOX_IMAGE"), // "" -> alpine; set to e.g. python:3.12-slim for a real gate
		EgressDeny:    true,
		RequireDocker: requireDocker,
	}
	// F4 pivote: el ejecutor factory legacy (agent sandboxed + gate +
	// agentic_verify) queda APAGADO por default — la ejecución vive en GitHub.
	// VIBEFORGE_LEGACY_FACTORY=1 lo re-enciende (modo self-hosted). El pipeline
	// de diseño (design/human_gate/pr/ticket_publish) no se toca.
	legacyFactory := os.Getenv("VIBEFORGE_LEGACY_FACTORY") == "1"
	if legacyFactory {
		eng.Register("gate", hardGate)
	}

	// Load CLI backends from registry/backends/*.yaml. Each file defines one
	// engine (id + argv). Adding opencode, copilot-cli, or any other claude-code-
	// protocol CLI requires only a YAML file — zero Go. The "claude" entry is the
	// default; all others are opt-in via `backend: <id>` in the agent's manifest.
	registryBackends, berr := agent.LoadBackends(filepath.Join(cfg.RegistryRoot, "backends"))
	if berr != nil {
		log.Printf("app: warn: could not load registry backends: %v", berr)
		registryBackends = map[string]agent.Backend{}
	}

	// Env-based overrides: CURSOR_BASE_URL registers the Cursor local API
	// (OpenAI-compatible /chat/completions). Pure-generation only — no tool loop.
	if cursorURL := os.Getenv("CURSOR_BASE_URL"); cursorURL != "" {
		ob := agent.OpenAIBackend{BaseURL: cursorURL, APIKey: os.Getenv("CURSOR_API_KEY")}
		registryBackends["cursor"] = ob
		registryBackends["openai"] = ob
		log.Printf("app: registered cursor/openai backend at %s", cursorURL)
	}

	backend := cfg.Backend
	if backend == nil {
		if cfg.EngineMode == "claude" {
			// Prefer the registry entry so the argv is configurable via YAML.
			if b, ok := registryBackends["claude"]; ok {
				backend = b
			} else {
				backend = agent.CliBackend{} // zero-value defaults to claude argv
			}
		} else {
			backend = agent.FakeBackend{Reply: "stub: implemented"}
		}
	}
	// Register configured secrets for live-log redaction (audit C4): the agent's
	// auth token and the control-plane service token must never surface in a
	// persisted step.event. Token-prefix shapes are handled by the redactor itself.
	agent.RegisterSecret(cfg.AgentAuth.Token)
	agent.RegisterSecret(os.Getenv("VIBEFORGE_API_TOKEN"))

	agentRunner := agent.NewStepRunner(backend, agentLoader)
	agentRunner.Auth = cfg.AgentAuth
	agentRunner.Backends = registryBackends
	// Cost routing (billing step 5): the margin lever. The policy assigns a cheaper
	// model to decomposition/planning agents and a capable one to complex work,
	// above the manifest default — lowering our token cost without changing price.
	agentRunner.RouteModel = func(stepType, agentID string) string {
		_, m := billing.DefaultPolicy.Resolve(stepType, agentID, "")
		return m
	}
	if cfg.AgentTimeout > 0 {
		agentRunner.Timeout = cfg.AgentTimeout
	}
	// Run the agent INSIDE the per-task sandbox (docker + egress allowlist), on a
	// .git-less worktree. The agent's image must contain the agent CLI
	// (VIBEFORGE_AGENT_IMAGE); the egress network (VIBEFORGE_SANDBOX_NETWORK)
	// has no internet gateway — only the egress-proxy is reachable.
	agentRunner.Sandboxed = true
	agentRunner.SandboxTemplate = sandbox.Config{
		Runtime:       cfg.SandboxRuntime,
		OCIRuntime:    os.Getenv("VIBEFORGE_SANDBOX_RUNTIME"),
		Image:         os.Getenv("VIBEFORGE_AGENT_IMAGE"),
		Network:       envOr("VIBEFORGE_SANDBOX_NETWORK", sandbox.DefaultEgressNetwork),
		UID:           os.Getenv("VIBEFORGE_SANDBOX_UID"),
		RequireDocker: requireDocker,
		// EgressDeny stays false: the agent NEEDS the LLM API (via the proxy).
	}
	agentRunner.Egress = agent.EgressConfig{
		ProxyURL:      os.Getenv("VIBEFORGE_EGRESS_PROXY_URL"),
		AnthropicBase: os.Getenv("VIBEFORGE_EGRESS_ANTHROPIC_URL"),
		Open:          os.Getenv("VIBEFORGE_EGRESS") == "open",
	}
	if legacyFactory {
		eng.Register("agent", agentRunner)
	}

	// Design step — same agent runner wiring but Sandboxed=false: design turns
	// produce documents, not code, so there is no need for a container worktree.
	// Inputs may carry `output: <relpath>` to persist the doc to disk.
	designRunner := agent.NewStepRunner(backend, agentLoader)
	designRunner.Auth = cfg.AgentAuth
	designRunner.Backends = registryBackends
	if cfg.AgentTimeout > 0 {
		designRunner.Timeout = cfg.AgentTimeout
	}
	designRunner.Sandboxed = false // design phases run on the host; no sandbox needed
	eng.Register("design", designRunner)

	verifyRunner := agent.NewVerifyRunner(backend, agentLoader)
	verifyRunner.Auth = cfg.AgentAuth
	if cfg.AgentTimeout > 0 {
		verifyRunner.Timeout = cfg.AgentTimeout
	}
	if legacyFactory {
		eng.Register("agentic_verify", verifyRunner)
	}

	eng.Register("human_gate", agent.HumanGateRunner{})
	eng.Register("pr", pr.NewRunner())

	// Release step (sprint-review preview): builds a project's branch and publishes a
	// navigable preview. The BUILD runs in the SAME docker sandbox as the agent (image
	// + egress allowlist + RequireDocker) so untrusted repo code never runs on the host
	// (audit C2). Static artifacts are served by the control plane from previewsRoot;
	// firebase deploys a Hosting preview channel. proj may be nil (config via inputs).
	previewsRoot := resolvePreviewsRoot(cfg.WorkdirRoot)
	previewSecret := resolvePreviewSecret()
	releaseRunner := &release.Runner{
		PreviewsRoot: previewsRoot,
		BaseURL:      os.Getenv("VIBEFORGE_PUBLIC_URL"),
		SandboxTemplate: sandbox.Config{
			Runtime:       cfg.SandboxRuntime,
			OCIRuntime:    os.Getenv("VIBEFORGE_SANDBOX_RUNTIME"),
			Image:         os.Getenv("VIBEFORGE_AGENT_IMAGE"),
			Network:       envOr("VIBEFORGE_SANDBOX_NETWORK", sandbox.DefaultEgressNetwork),
			UID:           os.Getenv("VIBEFORGE_SANDBOX_UID"),
			RequireDocker: requireDocker,
			// EgressDeny stays false: the build needs the npm registry / firebase via the proxy.
		},
		BuildTimeout:     time.Duration(envIntOr("VIBEFORGE_RELEASE_BUILD_TIMEOUT_MIN", 10)) * time.Minute,
		MaxArtifactBytes: int64(envIntOr("VIBEFORGE_RELEASE_MAX_MB", 200)) << 20,
		KeepPreviews:     envIntOr("VIBEFORGE_RELEASE_KEEP", 5),
		SecretSink:       agent.RegisterSecret, // firebase token → live-log redactor
	}
	eng.Register("release", releaseRunner)

	// Ticket store — optional. When TicketsDB is set, open the store, register
	// the ticket_publish step runner, and pass the store to the API server so
	// the HTTP ticket routes become active. When empty, the runner is not
	// registered and the API routes return 503 (needTickets guard).
	var tix *tickets.Store
	// La costura del journey: el hook OnPublished (export+scaffold tras el
	// diseño) se cablea cuando srv exista; el runner se guarda aquí.
	var pubRunner *tickets.PublishRunner
	if cfg.TicketsDB != "" {
		var tixErr error
		tix, tixErr = tickets.Open(cfg.TicketsDB)
		if tixErr != nil {
			_ = st.Close()
			return nil, fmt.Errorf("open tickets db: %w", tixErr)
		}
		pubRunner = &tickets.PublishRunner{Store: tix}
		eng.Register("ticket_publish", pubRunner)
		// plan_apply — the sprint-planning ceremony's apply step: parse a PLAN doc's
		// actions and mutate the ticket store transactionally, then stamp planned_at.
		eng.Register("plan_apply", &tickets.PlanApplyRunner{Store: tix})
	}

	// Project store — optional. When ProjectsDB is set, open the store and pass
	// it to the API server so POST/GET /projects routes become active.
	var proj *projects.Store
	if cfg.ProjectsDB != "" {
		var projErr error
		proj, projErr = projects.Open(cfg.ProjectsDB)
		if projErr != nil {
			_ = st.Close()
			if tix != nil {
				_ = tix.Close()
			}
			return nil, fmt.Errorf("open projects db: %w", projErr)
		}
		// The release runner resolves each project's release_target + firebase token.
		releaseRunner.Projects = proj
	}

	// Auth store — optional. When AuthDB is set, open it and seed the first admin
	// from VIBEFORGE_ADMIN_EMAIL/PASSWORD if the users table is empty. The store
	// is passed to the API server so /auth/* routes activate and to the
	// mandatory-auth middleware (wired in cmd/control).
	var au *auth.Store
	if cfg.AuthDB != "" {
		var auErr error
		au, auErr = auth.Open(cfg.AuthDB)
		if auErr != nil {
			_ = st.Close()
			if tix != nil {
				_ = tix.Close()
			}
			if proj != nil {
				_ = proj.Close()
			}
			return nil, fmt.Errorf("open auth db: %w", auErr)
		}
		if _, seedErr := auth.SeedAdmin(au); seedErr != nil {
			_ = au.Close()
			_ = st.Close()
			if tix != nil {
				_ = tix.Close()
			}
			if proj != nil {
				_ = proj.Close()
			}
			return nil, fmt.Errorf("seed admin: %w", seedErr)
		}
	}

	bus := api.NewBus(st)
	reg := api.NewRegistry(cfg.RegistryRoot)
	srv := api.NewServer(st, eng, bus, reg, tix, proj, au)
	srv.PreviewsRoot = previewsRoot // serve the release step's static previews
	srv.PreviewSecret = previewSecret
	srv.PreviewsBaseURL = os.Getenv("VIBEFORGE_PUBLIC_URL")

	var appApprover *conductor.Approver
	// GitHub projection (F1 pivot): mirror exported stories' state (issue/PR) into
	// the ticket store. The webhook secret is read per-request (env wins, settings
	// fallback) so it can be configured without a restart.
	if tix != nil {
		gh := github.New()
		srv.Projector = conductor.NewProjector(tix, gh)
		srv.Projector.TaskState = gh // barrido de sesiones muertas (F3)
		srv.Projector.Closer = gh    // cierre de loop de PRs mergeados sin auto-close
		srv.Projector.Files = gh     // grafo producto↔código: rutas del PR mergeado (task #5)
		// Al capturar archivos nuevos, regenerar docs/MODULE_MAP.md (Capa 1 mantenida);
		// en goroutine para no bloquear el pase de proyección con la escritura al repo.
		srv.Projector.OnGraphChanged = func(pid, repo string) { go srv.WriteModuleMap(pid, repo) }
		// Multi-tenant: cada proyecto opera con SU credencial (token del dueño
		// o installation token de la App); sin credenciales → auth del host.
		srv.Projector.ClientFor = srv.GHForProject
		srv.Dispatcher = &conductor.Dispatcher{Tickets: tix, GH: gh, ClientFor: srv.GHForProject}
		// JIT grooming (BMAD level-2): antes de despachar una story, el story-detailer
		// la expande a un spec dev-ready y enriquece el issue (spec + visual + componentes).
		// El agente corre en el HOST, sin sandbox (mismo cableado que el designRunner).
		// Default ON; VIBEFORGE_CONDUCTOR_GROOM=0 lo apaga (deja el body como lo exportó
		// export.go, byte-idéntico) para poder medir el impacto.
		if os.Getenv("VIBEFORGE_CONDUCTOR_GROOM") != "0" {
			srv.Dispatcher.Groomer = &conductor.Groomer{
				Backend: backend,
				Agents:  agentLoader,
				Auth:    cfg.AgentAuth,
				Tickets: tix,
				Timeout: cfg.AgentTimeout,
				ClientFor: func(ctx context.Context, projectID string) conductor.GroomGitHub {
					if c := srv.GHForProject(ctx, projectID); c != nil {
						return c
					}
					return nil
				},
			}
		}
		// Resolución de conflictos de PR (incidente #83): despacha al canal
		// claude_action del tenant; mismo ClientFor multi-tenant que el resto.
		srv.Resolver = conductor.NewConflictResolver(gh)
		srv.Resolver.ClientFor = srv.GHForProject
		if pubRunner != nil {
			pubRunner.OnPublished = srv.OnBacklogPublished
		}
		appApprover = &conductor.Approver{GH: gh}
		srv.GHWebhookSecret = func() string {
			if v := os.Getenv("VIBEFORGE_GITHUB_WEBHOOK_SECRET"); v != "" {
				return v
			}
			return srv.Settings.MCPValue("github", "webhook_secret")
		}
	}

	// Billing (SaaS metering + entitlements). Always on: it auto-creates a free
	// workspace per owner. Meter (a) real token cost is accrued in the usage handler;
	// meter (b) billable features via the ticket store's done hook (catches story- AND
	// sprint-mode merges), resolving the workspace from the project's owner.
	bill, billErr := billing.Open(filepath.Join(filepath.Dir(cfg.DBPath), "billing.db"))
	if billErr != nil {
		return nil, fmt.Errorf("billing store: %w", billErr)
	}
	srv.Billing = bill
	if tix != nil && proj != nil {
		tix.OnStoryDone = func(projectID, storyID, runID string) {
			p, err := proj.Get(projectID)
			if err != nil || p.OwnerID == "" {
				return // unowned/system run → not billable
			}
			ws, err := bill.WorkspaceForOwner(p.OwnerID)
			if err != nil {
				return
			}
			_, _ = bill.CountFeature(ws.ID, storyID, runID)
		}
	}

	// Channel delivery (v1.3): fan notable factory events (run failed/awaiting/done,
	// spend cap) to each project's subscribed channels. Telegram first; its bot token
	// is read per-send from settings (settings.mcp.telegram.token), so a token change
	// is picked up without a restart. Sends are async + best-effort (Delivery).
	var digestChannels channels.Registry
	if proj != nil {
		tg := telegram.New(func() string { return srv.Settings.MCPValue("telegram", "token") })
		registry := channels.Registry{tg.Name(): tg}
		digestChannels = registry // reused by the daily-digest scheduler (task #digest)
		srv.Channels = registry   // inbound webhooks reply through the same connectors
		delivery := &channels.Delivery{
			Registry: registry,
			Lookup: func(projectID, eventType string) ([]channels.Target, error) {
				cs, err := proj.ChannelsForEvent(projectID, eventType)
				if err != nil {
					return nil, err
				}
				out := make([]channels.Target, 0, len(cs))
				for _, c := range cs {
					out = append(out, channels.Target{Connector: c.Connector, Target: c.Target})
				}
				return out, nil
			},
			Logf: log.Printf,
		}
		bus.SetOnEvent(func(ev store.Event) {
			delivery.Deliver(ev.Type, ev.ProjectID, ev.RunID, []byte(ev.Data))
		})
		// Keep the bot token out of logs (log-redaction layer).
		if tok := srv.Settings.MCPValue("telegram", "token"); tok != "" {
			agent.RegisterSecret(tok)
		}
	}

	// Daily digest (standup push): the Digester gathers the standup facts from the
	// ticket + control stores; spend comes from billing (project → owner → workspace).
	// It is shared by the Brain's daily_digest tool and the scheduler below. The LLM
	// synthesis is wired in the Brain block (only when a key is present); without it
	// the digest ships the deterministic four-section render.
	var digester *digest.Digester
	if tix != nil {
		digester = &digest.Digester{Stories: tix, Runs: st}
		if proj != nil {
			digester.Spend = func(projectID string, since int64) (float64, error) {
				p, err := proj.Get(projectID)
				if err != nil || p.OwnerID == "" {
					return 0, nil // unowned/system project → no billable spend
				}
				ws, err := bill.WorkspaceForOwner(p.OwnerID)
				if err != nil {
					return 0, nil
				}
				return bill.SpendSince(ws.ID, since)
			}
		}
	}

	// Brain (optional): the per-project conversational assistant. Wired only when a
	// dedicated ANTHROPIC_API_KEY is present; otherwise its routes return 503. It
	// runs the tool-use loop in-process against the engine/store, and streams over
	// the same event bus (emit → AppendEvent scoped to a conversation id). Its LLM
	// also powers the digest's synthesis when present.
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		bst, bErr := brain.Open(filepath.Join(filepath.Dir(cfg.DBPath), "brain.db"))
		if bErr != nil {
			return nil, fmt.Errorf("brain store: %w", bErr)
		}
		llm := brain.NewClient(key, os.Getenv("BRAIN_MODEL"))
		if digester != nil {
			digester.Synthesize = func(ctx context.Context, system, user string) (string, error) {
				res, err := llm.Stream(ctx, system,
					[]brain.Message{{Role: "user", Content: []brain.ContentBlock{{Type: "text", Text: user}}}}, nil, nil)
				if err != nil {
					return "", err
				}
				return res.Text, nil
			}
		}
		ops := brain.EngineOps{Engine: eng, Store: st, Tickets: tix, RegistryDir: cfg.RegistryRoot, Digester: digester}
		if proj != nil {
			ops.ProjectInfo = func(projectID string) (string, time.Time) {
				p, err := proj.Get(projectID)
				if err != nil {
					return projectID, time.Time{}
				}
				name := p.Name
				if name == "" {
					name = projectID
				}
				return name, digestSince(p.LastDigestAt)
			}
		}
		emit := func(convID, typ string, data map[string]any) { _, _ = st.AppendEvent(convID, "", typ, data) }
		srv.Brain = brain.New(llm, ops, bst, emit)
	}

	// Digest schedule: VIBEFORGE_DIGEST_CRON (5-field cron, UTC; default 08:00 Panamá
	// = 13:00 UTC). Parsed at boot so a bad expression FAILS the boot with a clear
	// message rather than silently never firing.
	digestSchedule, cronErr := cron.Parse(envOr("VIBEFORGE_DIGEST_CRON", "0 13 * * *"))
	if cronErr != nil {
		return nil, fmt.Errorf("VIBEFORGE_DIGEST_CRON: %w", cronErr)
	}

	workers := cfg.Workers
	if workers <= 0 {
		workers = 1
	}
	return &App{
		Store: st, Tickets: tix, Projects: proj, Auth: au, Engine: eng, Bus: bus, Server: srv,
		workers: workers, approver: appApprover, PreviewSecret: previewSecret,
		digester: digester, digestChannels: digestChannels, digestCron: digestSchedule,
	}, nil
}

// resolvePreviewSecret returns the HMAC key for preview capability tokens.
// VIBEFORGE_PREVIEW_SECRET pins it (required for multi-instance deploys so tokens
// verify across replicas); otherwise a random per-process key is generated — tokens
// then simply stop verifying after a restart, which is fine given their ~10m TTL.
func resolvePreviewSecret() []byte {
	if v := os.Getenv("VIBEFORGE_PREVIEW_SECRET"); v != "" {
		return []byte(v)
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is catastrophic and near-impossible; fail loud rather
		// than sign tokens with a predictable key.
		log.Fatalf("app: cannot generate preview-token secret: %v", err)
	}
	return b
}

// digestSince converts a stored last_digest_at (unix millis; 0 = never) into the
// window start the digest reports from — the zero Time for "never" so the first
// digest covers all history rather than everything since 1970.
func digestSince(lastDigestAt int64) time.Time {
	if lastDigestAt <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(lastDigestAt)
}

// ClaudeBackend returns a CliBackend pre-configured for the claude CLI.
// Kept for test compatibility; production code uses the registry loader.
func ClaudeBackend() agent.Backend { return agent.CliBackend{} }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envIntOr reads an int env var, falling back to def on absent/unparseable.
func envIntOr(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// resolvePreviewsRoot picks the directory the release step publishes static previews
// to (and the API serves from). VIBEFORGE_PREVIEWS_DIR overrides; the default is a
// sibling of the workdir root (NOT under it — the retention GC prunes workdirs, and a
// preview must outlive its ephemeral run). Absolute so the API/serve paths are stable.
func resolvePreviewsRoot(workdirRoot string) string {
	dir := os.Getenv("VIBEFORGE_PREVIEWS_DIR")
	if dir == "" {
		base := filepath.Dir(workdirRoot)
		if base == "" || base == "." {
			dir = ".vibeforge-previews"
		} else {
			dir = filepath.Join(base, "vibeforge-previews")
		}
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// dockerRequired decides whether the agent/gate must hard-fail when docker
// isolation is unavailable (audit C2/C2b). It is true when the operator selected
// the docker sandbox runtime or set VIBEFORGE_REQUIRE_SANDBOX=1 — UNLESS they
// explicitly opted into the host fallback with VIBEFORGE_ALLOW_LOCAL_SANDBOX=1
// (dev/test only). The result is the secure default: an unconfigured docker-mode
// deployment refuses to degrade to the host.
func dockerRequired(sandboxRuntime string) bool {
	if os.Getenv("VIBEFORGE_ALLOW_LOCAL_SANDBOX") == "1" {
		return false // explicit dev/test opt-out
	}
	if os.Getenv("VIBEFORGE_REQUIRE_SANDBOX") == "1" {
		return true
	}
	return sandboxRuntime == "docker"
}

// StartBackground launches the in-process worker POOL, reaper and event bus.
// The pool gives parallel execution across independent runs/tasks: the atomic
// claim (BEGIN IMMEDIATE + fence) guarantees no two workers ever claim the same
// task, so N workers drain the ready queue concurrently. Steps within one run
// stay serial (step N+1 is enqueued only after N completes).
func (a *App) StartBackground(ctx context.Context) {
	for i := 0; i < a.workers; i++ {
		go a.Engine.WorkerLoop(ctx, fmt.Sprintf("inproc-worker-%d", i))
	}
	go a.Engine.ReaperLoop(ctx, 60_000, 10*time.Second)
	// Retention (audit A3): opt-in disk GC — prunes old events + terminal-run workdirs.
	// Off unless VIBEFORGE_RETENTION_DAYS is set (compose sets it for the deploy).
	if v := os.Getenv("VIBEFORGE_RETENTION_DAYS"); v != "" {
		if days, err := strconv.Atoi(v); err == nil && days > 0 {
			go a.Engine.RetentionLoop(ctx, time.Duration(days)*24*time.Hour, time.Hour)
		}
	}
	go a.Bus.Run(ctx)
	// GitHub conductor tick (F1+F2): sincroniza la proyección y, para proyectos en
	// dispatch_mode=auto, despacha el trabajo listo. Fallback de polling cuando no
	// llegan webhooks (dev/local). VIBEFORGE_GITHUB_SYNC_INTERVAL en segundos; 0
	// lo apaga; default 60s. Cuesta 2 llamadas gh por proyecto-con-repo por tick.
	if a.Server != nil && a.Server.Projector != nil && a.Projects != nil {
		// 25s: la ventana merge→proyección que el usuario percibe (con webhook
		// en producción esto es instantáneo; el poll es el fallback local).
		interval := 25 * time.Second
		if v := os.Getenv("VIBEFORGE_GITHUB_SYNC_INTERVAL"); v != "" {
			if secs, err := strconv.Atoi(v); err == nil {
				interval = time.Duration(secs) * time.Second
			}
		}
		if interval > 0 {
			go a.conductorLoop(ctx, interval)
		}
	}
	// Daily digest scheduler (standup push): fires on VIBEFORGE_DIGEST_CRON and
	// pushes each project's digest to its configured channel. Needs the digester,
	// the projects store, and a channel registry to deliver through.
	if a.digester != nil && a.Projects != nil && len(a.digestChannels) > 0 {
		go a.digestLoop(ctx)
	}
}

// digestLoop sleeps until the next cron tick, then sends the digest for every
// project that has a digest channel configured. It re-derives the next tick each
// iteration so a long delivery never drifts the schedule.
func (a *App) digestLoop(ctx context.Context) {
	log.Printf("digest: scheduler on (cron %q UTC)", a.digestCron.String())
	for {
		next := a.digestCron.Next(time.Now().UTC())
		if next.IsZero() {
			log.Printf("digest: schedule %q never matches — scheduler stopping", a.digestCron.String())
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		a.sendDigests(ctx)
	}
}

// sendDigests builds and delivers the digest for each project with a configured
// digest_channel, then advances that project's last-digest watermark. A project
// with no channel (null = off) is skipped; one bad channel never blocks the rest.
func (a *App) sendDigests(ctx context.Context) {
	ps, err := a.Projects.List()
	if err != nil {
		log.Printf("digest: list projects: %v", err)
		return
	}
	for _, p := range ps {
		if p.DigestChannel == "" {
			continue // null = digest off for this project
		}
		connector, target := parseDigestChannel(p.DigestChannel)
		conn := a.digestChannels.Get(connector)
		if conn == nil {
			log.Printf("digest: %s: unknown connector %q", p.ID, connector)
			continue
		}
		name := p.Name
		if name == "" {
			name = p.ID
		}
		text, err := a.digester.Build(ctx, p.ID, name, digestSince(p.LastDigestAt))
		if err != nil {
			log.Printf("digest: build %s: %v", p.ID, err)
			continue
		}
		ev := channels.Event{Type: "digest.daily", ProjectID: p.ID, Title: text}
		if err := conn.Notify(ctx, target, ev); err != nil {
			log.Printf("digest: deliver %s → %s:%s: %v", p.ID, connector, target, err)
			continue
		}
		if err := a.Projects.MarkDigestSent(p.ID, time.Now().UTC().UnixMilli()); err != nil {
			log.Printf("digest: mark sent %s: %v", p.ID, err)
		}
	}
}

// parseDigestChannel splits a stored digest_channel into (connector, target). A
// bare value (no "connector:" prefix) defaults to telegram — the only connector
// today — so "123456789" and "telegram:123456789" are equivalent.
func parseDigestChannel(v string) (connector, target string) {
	if i := strings.IndexByte(v, ':'); i >= 0 {
		return v[:i], v[i+1:]
	}
	return "telegram", v
}

// autoMergeFails acota los reintentos de auto-merge por PR (un PR que no
// mergea 3 veces queda para el humano; se limpia al reiniciar el proceso).
var autoMergeFails = map[string]int{}

// autoMerge mergea los PRs "limpios" de stories in_review de un proyecto con
// merge_mode=auto. Conservador: solo mergeStateStatus CLEAN y sin draft.
func (a *App) autoMerge(ctx context.Context, projectID, repoURL string) {
	stories, err := a.Tickets.ListStoriesByProject(projectID)
	if err != nil {
		return
	}
	gh := a.Server.GHForProject(ctx, projectID)
	seen := map[string]bool{}
	for _, st := range stories {
		if st.Status != tickets.StatusInReview || st.PRURL == "" || seen[st.PRURL] || st.ExternalRef == "" {
			continue
		}
		seen[st.PRURL] = true
		if autoMergeFails[st.PRURL] >= 3 {
			continue
		}
		n := prNumberFromURL(st.PRURL)
		if n == 0 {
			continue
		}
		info, err := gh.PRMergeInfo(ctx, repoURL, n)
		if err != nil || info.Draft || info.State != "OPEN" || info.MergeStateStatus != "CLEAN" || info.ReviewDecision == "CHANGES_REQUESTED" {
			continue
		}
		if err := gh.MergePR(ctx, repoURL, n); err != nil {
			autoMergeFails[st.PRURL]++
			log.Printf("conductor: auto-merge PR #%d falló (%d/3): %v", n, autoMergeFails[st.PRURL], err)
			continue
		}
		log.Printf("conductor: auto-merge PR #%d (%s) — la cascada sigue vía proyección", n, projectID)
	}
}

func prNumberFromURL(url string) int {
	i := strings.LastIndex(url, "/")
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(url[i+1:])
	if err != nil {
		return 0
	}
	return n
}

// conductorLoop is the poll-driven conductor tick: projection sync for every
// project with a repo, then auto-dispatch where the project's policy allows it.
// Webhook deliveries trigger the same sync out-of-band for faster reaction.
func (a *App) conductorLoop(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		ps, err := a.Projects.List()
		if err != nil {
			continue
		}
		for _, p := range ps {
			if p.Repo == "" {
				continue
			}
			res, err := a.Server.Projector.SyncProject(ctx, p.ID, p.Repo)
			if err != nil {
				log.Printf("conductor: sync %s: %v", p.ID, err)
				continue
			}
			if res.Changed > 0 {
				log.Printf("conductor: sync %s — %d/%d stories actualizadas desde GitHub", p.ID, res.Changed, res.Mirrored)
			}
			if res.Mirrored == 0 {
				continue
			}
			set, err := a.Projects.GetSettings(p.ID)
			if err != nil {
				continue
			}
			// Aprobación segura de workflows (F3): bajo auto_if_safe, los runs
			// action_required de PRs que NO tocan .github/workflows/** se
			// aprueban solos; los que sí, quedan para el humano.
			if set.WorkflowApproval == projects.WorkflowApprovalAutoSafe && a.approver != nil {
				approver := &conductor.Approver{GH: a.Server.GHForProject(ctx, p.ID)}
				if res, err := approver.SweepSafeApprovals(ctx, p.Repo); err == nil && len(res.Approved) > 0 {
					log.Printf("conductor: %s — %d workflows aprobados (safe), %d bloqueados", p.ID, len(res.Approved), len(res.Blocked))
				}
			}
			// Auto-merge (F4): bajo merge_mode=auto, un PR de story in_review con
			// mergeStateStatus CLEAN (checks verdes, sin conflictos, sin review
			// negativa) se mergea solo; el merge dispara la cascada vía la
			// proyección. Reintentos acotados por PR (in-memory).
			if set.MergeMode == projects.MergeModeAuto {
				a.autoMerge(ctx, p.ID, p.Repo)
			}
			if set.DispatchMode != projects.DispatchAuto {
				continue
			}
			pol := conductor.Policy{
				ExecutionUnit:  set.ExecutionUnit,
				DispatchMode:   set.DispatchMode,
				Executor:       set.Executor,
				ModelByLane:    set.ModelByLane,
				ExecutorByLane: set.ExecutorByLane,
				MaxConcurrency: set.MaxConcurrency,
			}
			cands, err := a.Server.Dispatcher.Candidates(ctx, p.ID, p.Repo, pol)
			if err != nil {
				log.Printf("conductor: candidates %s: %v", p.ID, err)
				continue
			}
			for _, c := range cands {
				dres, err := a.Server.Dispatcher.Dispatch(ctx, p.ID, p.Repo, pol, c.ID)
				if err != nil {
					log.Printf("conductor: auto-dispatch %s/%s: %v", p.ID, c.ID, err)
					continue
				}
				log.Printf("conductor: auto-dispatch %s → %s (%s, model=%s): %v",
					p.ID, c.ID, dres.Channel, dres.Model, dres.Dispatched)
			}
		}
	}
}

// Close releases resources.
func (a *App) Close() error {
	if a.Tickets != nil {
		_ = a.Tickets.Close()
	}
	if a.Projects != nil {
		_ = a.Projects.Close()
	}
	if a.Auth != nil {
		_ = a.Auth.Close()
	}
	return a.Store.Close()
}
