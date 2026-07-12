package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/command"
	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/db"
	executionpkg "github.com/abdul-hamid-achik/local-agent/internal/execution"
	"github.com/abdul-hamid-achik/local-agent/internal/ice"
	"github.com/abdul-hamid-achik/local-agent/internal/initcmd"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/logging"
	"github.com/abdul-hamid-achik/local-agent/internal/mcp"
	"github.com/abdul-hamid-achik/local-agent/internal/memory"
	"github.com/abdul-hamid-achik/local-agent/internal/permission"
	"github.com/abdul-hamid-achik/local-agent/internal/skill"
	"github.com/abdul-hamid-achik/local-agent/internal/ui"
)

var version = "dev"

type availabilityAwareRouter interface {
	SetAvailableModels([]string)
	ResolveAvailableModel(string) string
}

func main() {
	os.Exit(run())
}

func run() int {
	// Handle --version flag before flag.Parse (which may fail)
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-version" {
			fmt.Println(version)
			return 0
		}
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init":
			force := false
			for _, arg := range os.Args[2:] {
				if arg == "--force" || arg == "-force" {
					force = true
				}
			}
			if err := initcmd.Run(".", initcmd.Options{Force: force}); err != nil {
				fmt.Fprintf(os.Stderr, "init: %v\n", err)
				return 1
			}
			fmt.Println("AGENTS.md created successfully.")
			return 0
		case "logs":
			return handleLogs(os.Args[2:])
		}
	}

	qwenRouterFlag := flag.Bool("qwen-router", false, "use optimized Qwen model router (experimental)")
	modelFlag := flag.String("model", "", "override Ollama model")
	agentProfileFlag := flag.String("agent", "", "override agent profile")
	promptFlag := flag.String("p", "", "run in non-interactive mode: send prompt, print response, exit")
	yoloFlag := flag.Bool("yolo", false, "auto-approve all tool calls (skip permission prompts)")
	flag.Parse()

	cfg, agentsDir, err := config.LoadWithAgentsDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	if *modelFlag != "" {
		cfg.Ollama.Model = *modelFlag
	}
	if *agentProfileFlag != "" {
		cfg.AgentProfile = *agentProfileFlag
	}

	// Create router - use Qwen-optimized router if flag is set.
	var router config.ModelRouter
	if *qwenRouterFlag {
		fmt.Fprintf(os.Stderr, "Using Qwen-optimized model router (experimental)\n")
	}
	router = newModelRouter(&cfg.Model, *qwenRouterFlag)

	modelName := cfg.Ollama.Model
	modelPinned := shouldPinStartupModel(*modelFlag, cfg.Model.AutoSelect)
	if cfg.AgentProfile != "" && agentsDir != nil {
		if profile := agentsDir.GetAgent(cfg.AgentProfile); profile != nil {
			if profile.Model != "" {
				modelName = profile.Model
				modelPinned = true
			}
		}
	}
	// Discover the actual local Ollama inventory once. Routing uses this set to
	// degrade to an installed tier instead of selecting a missing 0.8B/4B model
	// from the static catalog. Cloud entries are excluded and byte sizes are
	// retained for central memory admission.
	modelManager := llm.NewModelManager(cfg.Ollama.BaseURL, cfg.Ollama.NumCtx)
	defer modelManager.Close()
	discoveryCtx, cancelDiscovery := context.WithTimeout(context.Background(), 2*time.Second)
	localInventory, discoveryErr := modelManager.ListLocalModelInventory(discoveryCtx)
	cancelDiscovery()
	localModels := make([]string, len(localInventory))
	for i, model := range localInventory {
		localModels[i] = model.Name
	}
	modelManager.ConfigureLocalInventory(cfg.Privacy.LocalOnly, localInventory, discoveryErr == nil)
	if discoveryErr != nil && cfg.Privacy.LocalOnly && *promptFlag != "" {
		fmt.Fprintf(os.Stderr, "model: local-only mode could not verify Ollama local weights: %v\n", discoveryErr)
		return 1
	}
	awareRouter, ok := router.(availabilityAwareRouter)
	if !ok {
		fmt.Fprintln(os.Stderr, "model router does not support local inventory")
		return 1
	}
	modelName, modelList, err := resolveStartupModel(
		modelName,
		modelPinned,
		cfg.Privacy.LocalOnly,
		&cfg.Model,
		localModels,
		discoveryErr == nil,
		awareRouter,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "model: %v\n", err)
		return 1
	}
	if discoveryErr != nil {
		fmt.Fprintf(os.Stderr, "warning: local model discovery failed: %v\n", discoveryErr)
	}

	if discoveryErr == nil || !cfg.Privacy.LocalOnly {
		if err := modelManager.SetCurrentModel(modelName); err != nil {
			fmt.Fprintf(os.Stderr, "set current model: %v\n", err)
			return 1
		}
	}

	var servers []config.ServerConfig
	if len(cfg.Servers) > 0 {
		servers = cfg.Servers
	} else if agentsDir != nil && agentsDir.HasMCP() {
		servers = agentsDir.GetMCPServers()
	}

	registry := mcp.NewRegistryWithVersion(version)
	defer registry.Close()

	ag := agent.New(modelManager, registry, cfg.Ollama.NumCtx)
	// The application always runs with durable execution tracking. Embedded
	// package users may opt out, but neither the TUI nor headless mode may send
	// provider work without a scoped SQLite ledger.
	ag.RequireExecutionLedger(true)
	ag.SetToolsConfig(cfg.Tools)
	ag.SetRouter(router)
	// Cap any single tool result so a runaway read/command can't blow the
	// (small, local) context window. ~96KB is generous for code/output.
	ag.AddToolHook(agent.NewSizeCapHook(96 * 1024))
	if wd, err := os.Getwd(); err == nil {
		ag.SetWorkDir(wd)
		if err := applyWorkspaceIgnore(ag, wd); err != nil {
			fmt.Fprintf(os.Stderr, "workspace policy: %v\n", err)
			return 1
		}
	}
	// Open SQLite database for permissions and stats.
	dbStore, err := db.Open()
	if err != nil {
		fmt.Fprintf(os.Stderr, "database: %v\nlocal-agent requires its private execution ledger and will not start without it.\n", err)
		return 1
	}
	ag.SetExecutionLedger(dbStore)
	defer func() {
		if err := dbStore.Close(); err != nil {
			log.Printf("warning: close database: %v", err)
		}
	}()
	// Register this after the database closer: defers run LIFO, so the active
	// turn joins and writes its final execution receipt before SQLite closes.
	defer ag.Close()

	// Set up tool permission checker.
	permChecker := permission.NewChecker(dbStore, *yoloFlag)
	ag.SetPermissionChecker(permChecker)

	// Enable non-destructive compaction + manual checkpoints when the DB is up.
	ag.SetCheckpointStore(dbStore, 0)

	workspace := currentWorkspace()
	iceStorePath := ""
	if cfg.ICE.Enabled {
		iceStorePath, err = resolveICEStorePath(workspace, cfg.ICE.StorePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config: %v\n", err)
			return 1
		}
	}
	var memStore *memory.Store
	var legacyMemoryNotice string
	if workspace == "" {
		log.Printf("warning: project memory disabled: workspace identity is unavailable")
	} else {
		legacyMemoryNotice = legacyMemoryQuarantineNotice(workspace)
		if legacyMemoryNotice != "" {
			if *promptFlag != "" {
				fmt.Fprintln(os.Stderr, "memory:", legacyMemoryNotice)
			} else {
				log.Printf("memory: %s", legacyMemoryNotice)
			}
		}
		memStore = memory.NewStore(memory.DefaultPathForWorkspace(workspace))
		if err := memStore.Err(); err != nil {
			log.Printf("warning: project memory disabled: %v", err)
			memStore = nil
		} else {
			ag.SetMemoryStore(memStore)
		}
	}

	skillDirs := []string{cfg.SkillsDir}
	if agentsDir != nil && len(agentsDir.Skills) > 0 {
		for _, s := range agentsDir.Skills {
			if s.Path != "" {
				skillDir := filepath.Dir(s.Path)
				if skillDir != "" {
					skillDirs = append(skillDirs, skillDir)
				}
			}
		}
	}

	skillMgr := skill.NewManager("")
	for _, dir := range skillDirs {
		if dir != "" {
			skillMgr.AddSearchPath(dir)
		}
	}
	if err := skillMgr.LoadAll(); err != nil {
		fmt.Fprintf(os.Stderr, "skills: %v\n", err)
		return 1
	}
	baseLoadedContext, err := buildBaseLoadedContext(agentsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "project instructions: %v\n", err)
		return 1
	}

	// Non-interactive / pipe mode: run a single prompt and exit.
	if *promptFlag != "" {
		ctx, cancelRun := context.WithCancel(context.Background())
		defer cancelRun()
		headlessSignals := make(chan os.Signal, 1)
		headlessSignalDone := make(chan struct{})
		signal.Notify(headlessSignals, os.Interrupt, syscall.SIGTERM)
		go func() {
			select {
			case <-headlessSignals:
				// One signal requests a graceful cancellation. Restore the OS
				// default immediately so a wedged backend cannot swallow a second.
				signal.Stop(headlessSignals)
				cancelRun()
			case <-headlessSignalDone:
			}
		}()
		defer func() {
			signal.Stop(headlessSignals)
			close(headlessSignalDone)
		}()
		if err := applyInitialAgentProfile(ag, skillMgr, modelManager, agentsDir, baseLoadedContext, cfg.AgentProfile); err != nil {
			fmt.Fprintf(os.Stderr, "agent profile: %v\n", err)
			return 1
		}
		buildMode := ui.DefaultModeConfigs()[ui.ModeBuild]
		if explicitRouter, ok := router.(interface{ SetModeContext(config.ModeContext) }); ok {
			explicitRouter.SetModeContext(buildMode.RouterMode)
		}
		routedModel := selectHeadlessModel(modelName, *promptFlag, modelPinned, router, buildMode.RouterMode)
		if routedModel != modelName {
			modelName = routedModel
			if modelName == "" {
				fmt.Fprintln(os.Stderr, "model routing failed: no compatible local chat model is installed")
				return 1
			}
			if cfg.Privacy.LocalOnly && discoveryErr == nil {
				if err := config.CheckModelAvailableLocally(modelName, localModels); err != nil {
					fmt.Fprintf(os.Stderr, "model routing failed: %v\n", err)
					return 1
				}
			}
			if err := modelManager.SetCurrentModel(modelName); err != nil {
				fmt.Fprintf(os.Stderr, "model routing failed: %v\n", err)
				return 1
			}
		}

		// Ping Ollama synchronously.
		fmt.Fprintf(os.Stderr, "connecting to Ollama (%s)...\n", modelName)
		if err := modelManager.Ping(); err != nil {
			fmt.Fprintf(os.Stderr, "ollama: %v\nhint: is `ollama serve` running? is %q pulled?\n", err, modelName)
			return 1
		}

		// Connect MCP servers synchronously.
		var wg sync.WaitGroup
		for _, srv := range servers {
			wg.Add(1)
			go func(s config.ServerConfig) {
				defer wg.Done()
				fmt.Fprintf(os.Stderr, "connecting MCP server %s...\n", s.Name)
				if _, err := registry.ConnectServer(ctx, s); err != nil {
					fmt.Fprintf(os.Stderr, "MCP server %s failed: %v\n", s.Name, err)
				}
			}(srv)
		}
		wg.Wait()

		// ICE setup.
		if cfg.ICE.Enabled && workspace != "" {
			iceEngine, err := ice.NewEngine(modelManager, memStore, resolvedICEEngineConfig(cfg, workspace, iceStorePath))
			if err != nil {
				fmt.Fprintf(os.Stderr, "ICE: %v\n", err)
			} else {
				ag.SetICEEngine(iceEngine)
				preview, previewErr := iceEngine.PreviewLegacyEntries()
				switch {
				case previewErr != nil:
					fmt.Fprintf(os.Stderr, "ICE: legacy history remains quarantined: %v\n", previewErr)
				case !preview.AlreadyClaimed && preview.Count > 0:
					fmt.Fprintf(os.Stderr, "ICE: %d provenance-free history entries quarantined; open the TUI and run /migrate-ice to explicitly attribute them\n", preview.Count)
				}
			}
		} else if cfg.ICE.Enabled {
			fmt.Fprintln(os.Stderr, "ICE: disabled because workspace identity is unavailable")
		}

		// Set BUILD mode for headless execution.
		ag.SetModeContext(buildMode.SystemPromptPrefix, buildMode.ToolPolicy)
		if workspace == "" {
			fmt.Fprintln(os.Stderr, "local-agent: workspace identity is unavailable; refusing to start a headless turn")
			return 1
		}
		session, err := dbStore.CreateSession(ctx, db.CreateSessionParams{
			Title:       headlessSessionTitle(*promptFlag),
			Model:       modelName,
			Mode:        buildMode.Label,
			WorkspaceID: workspace,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "local-agent: create execution session: %v\n", err)
			return 1
		}
		executionLease, err := dbStore.AcquireExecutionSessionLease(ctx, session.ID, workspace)
		if err != nil {
			cleanupErr := dbStore.DeleteSession(context.Background(), session.ID)
			fmt.Fprintf(os.Stderr, "local-agent: lock execution session: %v\n", errors.Join(err, cleanupErr))
			return 1
		}
		defer func() {
			ag.Close()
			if err := executionLease.Close(); err != nil {
				log.Printf("warning: release execution session: %v", err)
			}
		}()
		ag.SetCheckpointSessionID(session.ID)
		ag.SetExecutionSessionID(session.ID)
		ag.SetExecutionSnapshotCursor(0)

		// Run the agent synchronously.
		out := agent.NewHeadlessOutput()
		ag.AddUserMessage(*promptFlag)
		persistHeadlessState := func(saveCtx context.Context, executionCursor int64) error {
			stateJSON, encodeErr := ui.EncodeHeadlessSessionState(
				ag.Messages(), modelName, cfg.AgentProfile, modelPinned, executionCursor,
			)
			if encodeErr != nil {
				return encodeErr
			}
			return dbStore.SaveSessionState(saveCtx, session.ID, stateJSON)
		}
		if err := persistHeadlessState(ctx, 0); err != nil {
			leaseErr := executionLease.Close()
			cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), time.Second)
			cleanupErr := dbStore.DeleteSession(cleanupCtx, session.ID)
			cancelCleanup()
			fmt.Fprintf(os.Stderr, "local-agent: save execution session before dispatch: %v\n", err)
			if cleanupFailure := errors.Join(leaseErr, cleanupErr); cleanupFailure != nil {
				fmt.Fprintf(os.Stderr, "local-agent: remove incomplete execution session: %v\n", cleanupFailure)
			}
			return 1
		}
		runErr := ag.Run(ctx, out)
		saveCtx, cancelSave := context.WithTimeout(context.Background(), 2*time.Second)
		finalCursor := int64(0)
		finalCursor, cursorErr := headlessSnapshotExecutionCursor(saveCtx, dbStore, ag, session.ID, workspace, 0)
		saveErr := persistHeadlessState(saveCtx, finalCursor)
		cancelSave()
		saveErr = errors.Join(cursorErr, saveErr)
		if runErr != nil {
			if saveErr != nil {
				fmt.Fprintf(os.Stderr, "local-agent: save execution session after failure: %v\n", saveErr)
			}
			fmt.Fprintf(os.Stderr, "local-agent: %v\n", runErr)
			return 1
		}
		if saveErr != nil {
			fmt.Fprintf(os.Stderr, "local-agent: save completed execution session: %v\n", saveErr)
			return 1
		}
		return 0
	}

	cmdReg := command.NewRegistry()
	command.RegisterBuiltins(cmdReg)

	// Load custom commands from ~/.config/local-agent/commands/
	if home, err := os.UserHomeDir(); err == nil {
		customDir := filepath.Join(home, ".config", "local-agent", "commands")
		if err := command.RegisterCustomCommands(cmdReg, customDir); err != nil {
			fmt.Fprintf(os.Stderr, "Custom commands: %v\n", err)
		}
	}

	var agentList []string
	if agentsDir != nil {
		for _, a := range agentsDir.ListAgents() {
			agentList = append(agentList, a.Name)
		}
	}

	completer := ui.NewCompleter(cmdReg, modelList, skillMgr.Names(), agentList, registry)

	logger, logFile, err := logging.NewSessionLogger()
	if err != nil {
		log.Printf("warning: session logger: %v", err)
	}
	if logFile != nil {
		defer func() {
			if err := logFile.Close(); err != nil {
				log.Printf("warning: close log file: %v", err)
			}
		}()
	}

	if logger != nil {
		ag.SetLogger(logger)
	}

	m := ui.New(ag, cmdReg, skillMgr, completer, modelManager, router, logger)
	defer func() {
		ag.Close()
		if err := m.ReleaseExecutionSessionLease(); err != nil {
			log.Printf("warning: release execution session: %v", err)
		}
	}()
	m.SetModelPinned(modelPinned)
	m.SetSessionStore(dbStore)
	m.SetAgentProfileSource(agentsDir, baseLoadedContext, cfg.AgentProfile)
	// Bubble Tea's built-in signal handler exits before Model.Update sees the
	// event. Own SIGINT/SIGTERM so every OS shutdown follows the same
	// cancel/join/persist path as the in-app quit binding.
	p := tea.NewProgram(m, tea.WithoutSignalHandler())
	m.SetProgram(p)

	// Raw Ctrl+C remains a Bubble Tea key event; terminal-delivered SIGINT and
	// orchestrator SIGTERM are converted to the graceful shutdown message.
	sigCh := make(chan os.Signal, 1)
	signalDone := make(chan struct{})
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			// Forward one graceful request, then restore the OS default. If a
			// kernel syscall or third-party transport defeats cancellation, a
			// second signal remains an emergency termination path.
			signal.Stop(sigCh)
			p.Send(ui.ShutdownMsg{})
		case <-signalDone:
		}
	}()

	// Background initialization goroutine.
	initCtx, initCancel := context.WithCancel(context.Background())
	m.SetInitCancel(initCancel)
	initDone := make(chan struct{})

	go func() {
		defer close(initDone)
		if legacyMemoryNotice != "" {
			p.Send(ui.StartupStatusMsg{ID: "legacy:memory", Label: "Legacy memory", Status: "failed", Detail: legacyMemoryNotice})
		}

		// 1. Ping Ollama.
		p.Send(ui.StartupStatusMsg{ID: "ollama", Label: "Ollama (" + modelName + ")", Status: "connecting"})
		if err := modelManager.Ping(); err != nil {
			p.Send(ui.StartupStatusMsg{ID: "ollama", Label: "Ollama (" + modelName + ")", Status: "failed", Detail: err.Error()})
			p.Send(ui.ErrorMsg{Msg: fmt.Sprintf("ollama: %v\nhint: is `ollama serve` running? is %q pulled?", err, modelName)})
			// Continue — non-fatal for TUI, user can see the error.
		} else {
			p.Send(ui.StartupStatusMsg{ID: "ollama", Label: "Ollama (" + modelName + ")", Status: "connected"})
		}

		// 2. Connect MCP servers in parallel.
		if initCtx.Err() != nil {
			return
		}
		var wg sync.WaitGroup
		for _, srv := range servers {
			wg.Add(1)
			go func(s config.ServerConfig) {
				defer wg.Done()
				p.Send(ui.StartupStatusMsg{ID: "mcp:" + s.Name, Label: s.Name, Status: "connecting"})
				if initCtx.Err() != nil {
					return
				}
				toolCount, err := registry.ConnectServer(initCtx, s)
				if err != nil {
					p.Send(ui.StartupStatusMsg{ID: "mcp:" + s.Name, Label: s.Name, Status: "failed", Detail: err.Error()})
				} else {
					p.Send(ui.StartupStatusMsg{ID: "mcp:" + s.Name, Label: s.Name, Status: "connected", Detail: fmt.Sprintf("%d tools", toolCount)})
				}
			}(srv)
		}
		wg.Wait()

		if initCtx.Err() != nil {
			return
		}

		// Start background health monitoring so a crashed MCP server is
		// auto-reconnected instead of staying dead until restart. Bound to
		// initCtx, so it stops cleanly when the TUI exits.
		registry.StartHealthMonitor(initCtx, mcp.MonitorConfig{}, func(s string) {
			if logger != nil {
				logger.Info("mcp health", "detail", s)
			}
		})

		// 3. ICE setup.
		var iceEnabled bool
		var iceConversations int
		var iceSessionID string
		if cfg.ICE.Enabled && workspace != "" {
			p.Send(ui.StartupStatusMsg{ID: "ice", Label: "ICE", Status: "connecting"})
			iceEngine, err := ice.NewEngine(modelManager, memStore, resolvedICEEngineConfig(cfg, workspace, iceStorePath))
			if err != nil {
				p.Send(ui.StartupStatusMsg{ID: "ice", Label: "ICE", Status: "failed", Detail: err.Error()})
			} else {
				ag.SetICEEngine(iceEngine)
				iceEnabled = true
				iceConversations = iceEngine.ScopedEntryCount()
				iceSessionID = iceEngine.SessionID()
				detail := fmt.Sprintf("%d scoped conversations", iceConversations)
				preview, previewErr := iceEngine.PreviewLegacyEntries()
				switch {
				case previewErr != nil:
					detail += "; legacy history quarantined: " + previewErr.Error()
				case !preview.AlreadyClaimed && preview.Count > 0:
					detail += fmt.Sprintf("; %d legacy entries quarantined — /migrate-ice", preview.Count)
				}
				p.Send(ui.StartupStatusMsg{ID: "ice", Label: "ICE", Status: "connected", Detail: detail})
			}
		} else if cfg.ICE.Enabled {
			p.Send(ui.StartupStatusMsg{ID: "ice", Label: "ICE", Status: "failed", Detail: "workspace identity unavailable; legacy retrieval disabled"})
		}

		// 4. Load context and agent profile.
		activeAgentProfile := cfg.AgentProfile
		if err := applyInitialAgentProfile(ag, skillMgr, modelManager, agentsDir, baseLoadedContext, cfg.AgentProfile); err != nil {
			ag.DenyAllMCPTools()
			activeAgentProfile = ""
			p.Send(ui.ErrorMsg{Msg: fmt.Sprintf("agent profile: %v", err)})
		}

		// 5. Collect results and send InitCompleteMsg.
		var failedServers []ui.FailedServer
		for _, fs := range registry.FailedServers() {
			failedServers = append(failedServers, ui.FailedServer{
				Name:   fs.Name,
				Reason: fs.Reason,
			})
		}

		p.Send(ui.InitCompleteMsg{
			Model:            modelName,
			ModelList:        modelList,
			AgentProfile:     activeAgentProfile,
			AgentList:        agentList,
			ToolCount:        ag.ToolCount(),
			ServerCount:      registry.ServerCount(),
			NumCtx:           cfg.Ollama.NumCtx,
			FailedServers:    failedServers,
			ICEEnabled:       iceEnabled,
			ICEConversations: iceConversations,
			ICESessionID:     iceSessionID,
		})
	}()

	_, runErr := p.Run()
	signal.Stop(sigCh)
	close(signalDone)
	initCancel()
	<-initDone
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "tui: %v\n", runErr)
		return 1
	}
	return 0
}

// currentWorkspace returns the process workspace used to scope local memory.
func currentWorkspace() string {
	workDir, err := os.Getwd()
	if err != nil {
		return ""
	}
	absolute, err := filepath.Abs(workDir)
	if err != nil {
		return ""
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute = resolved
	}
	return filepath.Clean(absolute)
}

func headlessSessionTitle(prompt string) string {
	title := strings.TrimSpace(strings.SplitN(prompt, "\n", 2)[0])
	if title == "" {
		title = "Headless session " + time.Now().Format("2006-01-02 15:04")
	}
	runes := []rune(title)
	if len(runes) > 72 {
		title = string(runes[:69]) + "..."
	}
	return title
}

func headlessSnapshotExecutionCursor(ctx context.Context, store *db.Store, ag *agent.Agent, sessionID int64, workspaceID string, current int64) (int64, error) {
	hazards, err := store.ListExecutionRecoveryHazards(ctx, sessionID, workspaceID, current, 100)
	if err != nil {
		return current, fmt.Errorf("inspect execution projection: %w", err)
	}
	messages := ag.Messages()
	for _, state := range hazards {
		if state.Latest.Type != executionpkg.EventCompleted {
			continue
		}
		projected := false
		for _, message := range messages {
			if message.Role == "tool" && message.ToolCallID == state.Identity.CanonicalCallID {
				projected = true
				break
			}
		}
		if !projected {
			return current, fmt.Errorf("completed effect %s is absent from the headless snapshot", state.Identity.ExecutionID)
		}
	}
	latest, err := store.LatestExecutionEventID(ctx, sessionID, workspaceID)
	if err != nil {
		return current, fmt.Errorf("read execution cursor: %w", err)
	}
	return latest, nil
}

func applyWorkspaceIgnore(ag *agent.Agent, workDir string) error {
	ignore, err := config.LoadIgnoreFileWithError(workDir)
	if err != nil {
		return err
	}
	if ignore != nil && ag != nil {
		ag.SetIgnoreContent(ignore.Raw())
	}
	return nil
}

// legacyMemoryQuarantineNotice performs read-only startup inventory. Startup,
// including headless mode, must never assign provenance-free memory to the
// first working directory that happens to launch local-agent.
func legacyMemoryQuarantineNotice(workspace string) string {
	preview, err := memory.PreviewDefaultLegacyForWorkspace(workspace)
	if err != nil {
		return fmt.Sprintf("legacy memory remains quarantined: %v", err)
	}
	if !preview.AlreadyClaimed && preview.Count > 0 {
		return fmt.Sprintf("%d provenance-free memories quarantined; open the TUI and use /migrate-memory to preview explicit attribution", preview.Count)
	}
	return ""
}

// handleLogs implements the "logs" subcommand.
// With -f it execs tail -f on the latest log file; otherwise it lists recent sessions.
func handleLogs(args []string) int {
	follow := false
	for _, arg := range args {
		if arg == "-f" {
			follow = true
		}
	}

	if follow {
		latest, err := logging.LatestLogPath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "logs: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "following %s\n", latest)
		tailBin, err := exec.LookPath("tail")
		if err != nil {
			fmt.Fprintf(os.Stderr, "logs: tail not found: %v\n", err)
			return 1
		}
		// Replace the process with tail -f.
		if err := syscall.Exec(tailBin, []string{"tail", "-f", latest}, os.Environ()); err != nil {
			fmt.Fprintf(os.Stderr, "logs: exec tail: %v\n", err)
			return 1
		}
		return 0
	}

	// List recent log sessions.
	entries, err := logging.ListLogs(20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logs: %v\n", err)
		return 1
	}
	if len(entries) == 0 {
		fmt.Println("No log files found in", logging.LogDir())
		return 0
	}

	fmt.Printf("Recent sessions (%s):\n\n", logging.LogDir())
	for _, e := range entries {
		name := filepath.Base(e.Path)
		sizeKB := float64(e.Size) / 1024
		fmt.Printf("  %-30s  %s  %6.1f KB\n", name, e.ModTime.Format("2006-01-02 15:04:05"), sizeKB)
	}
	fmt.Printf("\nTip: run `local-agent logs -f` to follow the latest log.\n")
	return 0
}
