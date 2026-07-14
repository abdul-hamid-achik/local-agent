package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
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
	"github.com/abdul-hamid-achik/local-agent/internal/goaladvisor"
	"github.com/abdul-hamid-achik/local-agent/internal/ice"
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
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init":
			return handleInit(os.Args[2:])
		case "logs":
			return handleLogs(os.Args[2:])
		case "goal":
			return handleGoalCommand(os.Args[2:])
		case "execution":
			return handleExecutionCommand(os.Args[2:])
		case "help":
			return handleRootHelp(os.Args[2:], os.Args[0], os.Stdout, os.Stderr)
		}
	}

	options, err := parseRootOptions(os.Args[0], os.Args[1:], os.Stderr, os.Stdout)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return 2
	}
	if options.version {
		fmt.Println(version)
		return 0
	}
	if len(options.arguments) > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument %q; pass a headless request with -p/--prompt\n", options.arguments[0])
		return 2
	}
	writeLegacyYoloWarning(os.Stderr, options.legacyYolo)

	qwenRouterFlag := &options.qwenRouter
	modelFlag := &options.model
	agentProfileFlag := &options.agentProfile
	promptFlag := options.prompt
	modeFlag := &options.mode
	autoFlag := &options.auto
	planFlag := &options.plan
	skipApprovalsFlag := &options.skipApprovals
	legacyYoloFlag := &options.legacyYolo
	resumeFlag := options.resume
	promptProvided := options.promptProvided
	if promptProvided && strings.TrimSpace(promptFlag) == "" {
		fmt.Fprintln(os.Stderr, "prompt: -p/--prompt requires a non-empty value")
		return 2
	}
	resumeSelector, resumeRequested, err := resumeFlag.selector()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resume: %v\n", err)
		return 2
	}
	if err := validateResumeInvocation(resumeRequested, promptProvided); err != nil {
		fmt.Fprintf(os.Stderr, "resume: %v\n", err)
		return 2
	}
	resolvedMode, err := resolveAuthorityShortcut(
		*modeFlag,
		options.modeProvided,
		*autoFlag,
		*planFlag,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mode: %v\n", err)
		return 2
	}
	headlessMode, err := parseHeadlessMode(resolvedMode, promptProvided)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mode: %v\n", err)
		return 2
	}

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
	// Ollama owns model availability. Local Agent applies privacy, capability,
	// and memory policy to that inventory instead of inventing availability from
	// a static catalog.
	modelManager := llm.NewModelManager(cfg.Ollama.BaseURL, cfg.Ollama.NumCtx)
	defer modelManager.Close()
	discoveryCtx, cancelDiscovery := context.WithTimeout(context.Background(), 2*time.Second)
	ollamaInventory, discoveryErr := modelManager.ListOllamaModels(discoveryCtx)
	cancelDiscovery()
	if discoveryErr == nil {
		enrichCtx, cancelEnrich := context.WithTimeout(context.Background(), 2*time.Second)
		ollamaInventory = enrichOllamaCapabilities(enrichCtx, modelManager, ollamaInventory, &cfg.Model)
		cancelEnrich()
	}
	modelManager.ConfigureOllamaRuntimeInventory(cfg.Privacy.LocalOnly, ollamaInventory, discoveryErr == nil)
	if discoveryErr != nil && cfg.Privacy.LocalOnly && promptFlag != "" {
		fmt.Fprintf(os.Stderr, "model: local-only mode could not verify Ollama local weights: %v\n", discoveryErr)
		return 1
	}
	awareRouter, ok := router.(availabilityAwareRouter)
	if !ok {
		fmt.Fprintln(os.Stderr, "model router does not support local inventory")
		return 1
	}
	manualChatModels := manuallySelectableOllamaChatModels(ollamaInventory, cfg.Privacy.LocalOnly)
	autoChatModels := autoRoutableOllamaChatModels(ollamaInventory)
	modelName, modelList, err := resolveStartupModel(
		modelName,
		modelPinned,
		cfg.Privacy.LocalOnly,
		&cfg.Model,
		manualChatModels,
		autoChatModels,
		discoveryErr == nil,
		awareRouter,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "model: %v\n", err)
		return 1
	}
	if discoveryErr != nil {
		fmt.Fprintf(os.Stderr, "warning: Ollama model discovery failed: %v\n", discoveryErr)
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

	registry := mcp.NewRegistryWithVersion(version, mcp.WithLocalOnly(cfg.Privacy.LocalOnly))
	defer registry.Close()

	ag := agent.New(modelManager, registry, cfg.Ollama.NumCtx)
	// Derive any reduced-friction MCP contracts from the same host-owned server
	// configuration the registry will connect. Server annotations and model tool
	// names alone never establish trust.
	ag.SetTrustedLocalMCPServers(servers)
	// The application always runs with durable execution tracking. Embedded
	// package users may opt out, but neither the TUI nor headless mode may send
	// provider work without a scoped SQLite ledger.
	ag.RequireExecutionLedger(true)
	ag.SetToolsConfig(cfg.Tools)
	ag.SetRouter(router)
	expertConsultant, expertErr := newRuntimeExpertConsultant(cfg, modelManager, agentsDir, ollamaInventory)
	if expertErr != nil {
		fmt.Fprintf(os.Stderr, "warning: expert consultation disabled: %v\n", expertErr)
	} else if expertConsultant != nil {
		ag.SetExpertConsultant(expertConsultant)
	}
	// Cap any single tool result so a runaway read/command can't blow the
	// (small, local) context window. ~96KB is generous for code/output.
	ag.AddToolHook(agent.NewSizeCapHook(96 * 1024))
	if wd, err := os.Getwd(); err == nil {
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
	permChecker := permission.NewChecker(dbStore, *skipApprovalsFlag || *legacyYoloFlag)
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
	if workspace == "" {
		log.Printf("warning: project memory disabled: workspace identity is unavailable")
	} else {
		// Pre-workspace memory remains quarantined. Startup only opens the
		// canonical workspace-scoped store; historical inventory is deliberately
		// absent from the everyday transcript and command surface.
		memStore = memory.NewStore(memory.DefaultPathForWorkspace(workspace))
		if err := memStore.Err(); err != nil {
			log.Printf("warning: project memory disabled: %v", err)
			memStore = nil
		} else {
			ag.SetMemoryStore(memStore)
		}
	}

	skillMgr, err := newRuntimeSkillManager(agentsDir, cfg.Agents.AutoLoad)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skills: %v\n", err)
		return 1
	}
	if err := skillMgr.LoadAll(); err != nil {
		fmt.Fprintf(os.Stderr, "skills: %v\n", err)
		return 1
	}
	// One loader setup feeds both headless and TUI runs. Manual/profile
	// activation remains an independent, eager prompt-content mechanism.
	ag.SetSkillLoader(skillMgr)
	baseLoadedContext, err := buildBaseLoadedContext(agentsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "project instructions: %v\n", err)
		return 1
	}
	baseLoadedContext = appendLoadedContext(baseLoadedContext, buildHostConfigProjection(cfg, agentsDir, servers))

	// Non-interactive / pipe mode: run a single prompt and exit.
	if promptFlag != "" {
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
		modeConfig := ui.DefaultModeConfigs()[headlessMode]
		if explicitRouter, ok := router.(interface{ SetModeContext(config.ModeContext) }); ok {
			explicitRouter.SetModeContext(modeConfig.RouterMode)
		}
		routedModel := selectHeadlessModel(modelName, promptFlag, modelPinned, router, modeConfig.RouterMode)
		if routedModel != modelName {
			modelName = routedModel
			if modelName == "" {
				fmt.Fprintln(os.Stderr, "model routing failed: no compatible local chat model is installed")
				return 1
			}
			if discoveryErr == nil {
				if !containsModel(autoChatModels, modelName) {
					fmt.Fprintf(os.Stderr, "model routing failed: model %q is not admitted for automatic local routing\n", modelName)
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
			fmt.Fprintf(os.Stderr, "ollama: %v\ntry: ollama serve · ollama pull %s\n", err, modelName)
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
			}
		} else if cfg.ICE.Enabled {
			fmt.Fprintln(os.Stderr, "ICE: disabled because workspace identity is unavailable")
		}

		// Headless -p is one bounded turn. AUTO uses the same scoped authority as
		// the TUI; it is independent from the --skip-approvals posture.
		ag.SetModeContext(modeConfig.SystemPromptPrefix, modeConfig.ToolPolicy)
		ag.SetAuthorityMode(headlessAuthorityMode(headlessMode))
		if workspace == "" {
			fmt.Fprintln(os.Stderr, "local-agent: workspace identity is unavailable; refusing to start a headless turn")
			return 1
		}
		session, err := dbStore.CreateSession(ctx, db.CreateSessionParams{
			Title:       headlessSessionTitle(promptFlag),
			Model:       modelName,
			Mode:        modeConfig.Label,
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
		ag.AddUserMessage(promptFlag)
		sessionStateRevision := int64(0)
		persistHeadlessState := func(saveCtx context.Context, executionCursor int64) error {
			stateJSON, encodeErr := ui.EncodeHeadlessSessionState(
				ag.Messages(), modelName, cfg.AgentProfile, modelPinned, executionCursor,
			)
			if encodeErr != nil {
				return encodeErr
			}
			nextRevision, saveErr := saveHeadlessSessionStateCAS(
				saveCtx, dbStore, session.ID, sessionStateRevision, stateJSON,
			)
			if saveErr != nil {
				return saveErr
			}
			sessionStateRevision = nextRevision
			return nil
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
	if expertErr != nil {
		m.SetExpertRuntimeSetupFailed()
	}
	if permChecker.SkipsApprovals() {
		m.SetApprovalPosture(ui.ApprovalPostureSkipApprovals)
	} else {
		m.SetApprovalPosture(ui.ApprovalPosturePrompted)
	}
	if goalAdvisorConfigured(servers) {
		m.SetGoalAdvisor(goaladvisor.NewCortex(registry, ag.WorkDir(), "local-agent"))
	}
	defer func() {
		ag.Close()
		if err := m.ReleaseExecutionSessionLease(); err != nil {
			log.Printf("warning: release execution session: %v", err)
		}
	}()
	m.SetModelPinned(modelPinned)
	m.SetSessionStore(dbStore)
	if resumeRequested {
		if err := m.SetStartupSessionResume(resumeSelector); err != nil {
			fmt.Fprintf(os.Stderr, "resume: %v\n", err)
			return 2
		}
	}
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

		// 1. Ping Ollama.
		p.Send(ui.StartupStatusMsg{ID: "ollama", Label: "Ollama (" + modelName + ")", Status: "connecting"})
		if err := modelManager.Ping(); err != nil {
			p.Send(ui.StartupStatusMsg{ID: "ollama", Label: "Ollama (" + modelName + ")", Status: "failed", Detail: err.Error()})
			p.Send(ui.ErrorMsg{Msg: fmt.Sprintf("ollama: %v\ntry: ollama serve · ollama pull %s", err, modelName)})
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
		registry.StartHealthMonitor(initCtx, mcp.MonitorConfig{
			OnSnapshot: func(statuses []mcp.ConnectionStatus) {
				p.Send(ui.MCPStatusSnapshotMsg{Servers: projectMCPStatuses(statuses)})
			},
		}, func(s string) {
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
		var runningModels []llm.OllamaRunningModel
		ollamaVersion := ""
		if discoveryErr == nil {
			metadataCtx, cancelMetadata := context.WithTimeout(initCtx, 2*time.Second)
			runningModels, _ = modelManager.ListRunningOllamaModels(metadataCtx)
			ollamaVersion, _ = modelManager.OllamaVersion(metadataCtx)
			cancelMetadata()
		}
		ollamaModels := ui.BuildOllamaModelDescriptors(
			ollamaInventory, runningModels, modelName, cfg.Privacy.LocalOnly,
		)
		for index := range ollamaModels {
			ollamaModels[index].EffectiveContext = modelManager.ContextPolicy(ollamaModels[index].Name).Effective
		}
		failedServers := registry.FailedServers()
		failedServerProjection := make([]ui.FailedServer, 0, len(failedServers))
		for _, failed := range failedServers {
			failedServerProjection = append(failedServerProjection, ui.FailedServer{
				Name: failed.Name, Reason: failed.Reason,
			})
		}
		p.Send(ui.InitCompleteMsg{
			Model:                    modelName,
			ModelList:                modelList,
			OllamaModels:             ollamaModels,
			OllamaVersion:            ollamaVersion,
			LocalOnly:                cfg.Privacy.LocalOnly,
			OllamaInventoryAttempted: true,
			AgentProfile:             activeAgentProfile,
			AgentList:                agentList,
			ToolCount:                ag.ToolCount(),
			ServerCount:              registry.ServerCount(),
			NumCtx:                   modelManager.NumCtx(),
			FailedServers:            failedServerProjection,
			MCPServers:               projectMCPStatuses(registry.ConnectionStatuses()),
			ICEEnabled:               iceEnabled,
			ICEConversations:         iceConversations,
			ICESessionID:             iceSessionID,
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

func projectMCPStatuses(statuses []mcp.ConnectionStatus) []ui.MCPServerStatus {
	projected := make([]ui.MCPServerStatus, 0, len(statuses))
	for _, status := range statuses {
		projected = append(projected, ui.MCPServerStatus{
			Name: status.Name, Connected: status.Connected,
			ToolCount: status.ToolCount, Detail: status.LastError,
		})
	}
	return projected
}

func newRuntimeSkillManager(agentsDir *config.AgentsDir, autoLoad bool) (*skill.Manager, error) {
	if !autoLoad {
		return skill.NewManager(""), nil
	}
	if agentsDir == nil || strings.TrimSpace(agentsDir.Path) == "" {
		return nil, errors.New("shared agents directory was not loaded")
	}
	// The selected shared agents root owns profiles and skills together. Do
	// not mix it with Local Agent's private config/state directory.
	return skill.NewManager(filepath.Join(agentsDir.Path, "skills")), nil
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

func saveHeadlessSessionStateCAS(ctx context.Context, store *db.Store, sessionID, expectedRevision int64, stateJSON string) (int64, error) {
	record, err := store.SaveSessionStateCAS(ctx, sessionID, expectedRevision, stateJSON)
	if err != nil {
		return expectedRevision, err
	}
	return record.Revision, nil
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
			if message.Role == "tool" &&
				message.ToolCallID == state.Identity.CanonicalCallID &&
				executionpkg.HashText(message.Content) == state.Latest.ResultSHA256 {
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
	if ag != nil {
		ignoreContent := ""
		if ignore != nil {
			ignoreContent = ignore.Raw()
		}
		ag.SetWorkspacePolicy(workDir, ignoreContent)
	}
	return nil
}
