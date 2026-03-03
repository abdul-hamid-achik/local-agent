package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"

	tea "charm.land/bubbletea/v2"

	"github.com/abdul-hamid-achik/local-agent/internal/agent"
	"github.com/abdul-hamid-achik/local-agent/internal/command"
	"github.com/abdul-hamid-achik/local-agent/internal/config"
	"github.com/abdul-hamid-achik/local-agent/internal/db"
	"github.com/abdul-hamid-achik/local-agent/internal/ice"
	"github.com/abdul-hamid-achik/local-agent/internal/initcmd"
	"github.com/abdul-hamid-achik/local-agent/internal/llm"
	"github.com/abdul-hamid-achik/local-agent/internal/logging"
	"github.com/abdul-hamid-achik/local-agent/internal/mcp"
	"github.com/abdul-hamid-achik/local-agent/internal/memory"
	"github.com/abdul-hamid-achik/local-agent/internal/permission"
	"github.com/abdul-hamid-achik/local-agent/internal/skill"
	"github.com/abdul-hamid-achik/local-agent/internal/tui"
)

func main() {
	// Handle subcommands before flag parsing.
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
				os.Exit(1)
			}
			fmt.Println("AGENT.md created successfully.")
			return
		case "logs":
			handleLogs(os.Args[2:])
			return
		}
	}

	modelFlag := flag.String("model", "", "override Ollama model")
	agentProfileFlag := flag.String("agent", "", "override agent profile")
	promptFlag := flag.String("p", "", "run in non-interactive mode: send prompt, print response, exit")
	yoloFlag := flag.Bool("yolo", false, "auto-approve all tool calls (skip permission prompts)")
	flag.Parse()

	cfg, agentsDir, err := config.LoadWithAgentsDir()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *modelFlag != "" {
		cfg.Ollama.Model = *modelFlag
	}
	if *agentProfileFlag != "" {
		cfg.AgentProfile = *agentProfileFlag
	}

	router := config.NewRouter(&cfg.Model)

	modelName := cfg.Ollama.Model
	if cfg.AgentProfile != "" && agentsDir != nil {
		if profile := agentsDir.GetAgent(cfg.AgentProfile); profile != nil {
			if profile.Model != "" {
				modelName = profile.Model
			}
		}
	}

	// Fast, non-blocking setup.
	modelManager := llm.NewModelManager(cfg.Ollama.BaseURL, cfg.Ollama.NumCtx)
	modelManager.SetCurrentModel(modelName)

	var servers []config.ServerConfig
	if len(cfg.Servers) > 0 {
		servers = cfg.Servers
	} else if agentsDir != nil && agentsDir.HasMCP() {
		servers = agentsDir.GetMCPServers()
	}

	registry := mcp.NewRegistry()
	defer registry.Close()

	ag := agent.New(modelManager, registry, cfg.Ollama.NumCtx)
	ag.SetRouter(router)
	if wd, err := os.Getwd(); err == nil {
		ag.SetWorkDir(wd)
	}
	defer ag.Close()

	// Open SQLite database for permissions and stats.
	dbStore, err := db.Open()
	if err != nil {
		log.Printf("warning: database: %v (permissions disabled)", err)
	}
	if dbStore != nil {
		defer dbStore.Close()
	}

	// Set up tool permission checker.
	permChecker := permission.NewChecker(dbStore, *yoloFlag)
	ag.SetPermissionChecker(permChecker)

	memStore := memory.NewStore("")
	ag.SetMemoryStore(memStore)

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
	_ = skillMgr.LoadAll()

	// Non-interactive / pipe mode: run a single prompt and exit.
	if *promptFlag != "" {
		ctx := context.Background()

		// Ping Ollama synchronously.
		fmt.Fprintf(os.Stderr, "connecting to Ollama (%s)...\n", modelName)
		if err := modelManager.Ping(); err != nil {
			fmt.Fprintf(os.Stderr, "ollama: %v\nhint: is `ollama serve` running? is %q pulled?\n", err, modelName)
			os.Exit(1)
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
		if cfg.ICE.Enabled {
			embedModel := cfg.ICE.EmbedModel
			if embedModel == "" {
				embedModel = cfg.Model.EmbedModel
			}
			iceEngine, err := ice.NewEngine(modelManager, memStore, ice.EngineConfig{
				EmbedModel: embedModel,
				StorePath:  cfg.ICE.StorePath,
				NumCtx:     cfg.Ollama.NumCtx,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "ICE: %v\n", err)
			} else {
				ag.SetICEEngine(iceEngine)
			}
		}

		// Load context and agent profile.
		if agentsDir != nil && agentsDir.GetGlobalInstructions() != "" {
			ag.SetLoadedContext(agentsDir.GetGlobalInstructions())
		}
		if data, err := os.ReadFile("AGENT.md"); err == nil {
			ag.AppendLoadedContext("\n\n" + string(data))
		}
		if cfg.AgentProfile != "" && agentsDir != nil {
			if profile := agentsDir.GetAgent(cfg.AgentProfile); profile != nil {
				if profile.SystemPrompt != "" {
					ag.AppendLoadedContext("\n\n" + profile.SystemPrompt)
				}
				for _, skillName := range profile.Skills {
					skillMgr.Activate(skillName)
				}
				ag.SetSkillContent(skillMgr.ActiveContent())
			}
		}

		// Set BUILD mode (tools enabled) for headless execution.
		modes := tui.DefaultModeConfigs()
		buildMode := modes[tui.ModeBuild]
		ag.SetModeContext(buildMode.SystemPromptPrefix, buildMode.AllowTools)

		// Run the agent synchronously.
		out := agent.NewHeadlessOutput()
		ag.AddUserMessage(*promptFlag)
		ag.Run(ctx, out)
		return
	}

	cmdReg := command.NewRegistry()
	command.RegisterBuiltins(cmdReg)

	// Load custom commands from ~/.config/local-agent/commands/
	if home, err := os.UserHomeDir(); err == nil {
		customDir := filepath.Join(home, ".config", "local-agent", "commands")
		command.RegisterCustomCommands(cmdReg, customDir)
	}

	var modelList []string
	for _, m := range cfg.Model.Models {
		modelList = append(modelList, m.Name)
	}

	var agentList []string
	if agentsDir != nil {
		for _, a := range agentsDir.ListAgents() {
			agentList = append(agentList, a.Name)
		}
	}

	completer := tui.NewCompleter(cmdReg, modelList, skillMgr.Names(), agentList, registry)

	logger, logFile, err := logging.NewSessionLogger()
	if err != nil {
		// Non-fatal; logging disabled.
	}
	if logFile != nil {
		defer logFile.Close()
	}

	m := tui.New(ag, cmdReg, skillMgr, completer, modelManager, router, logger)
	p := tea.NewProgram(m)
	m.SetProgram(p)

	// Background initialization goroutine.
	initCtx, initCancel := context.WithCancel(context.Background())
	m.SetInitCancel(initCancel)
	initDone := make(chan struct{})

	go func() {
		defer close(initDone)

		// 1. Ping Ollama.
		p.Send(tui.StartupStatusMsg{ID: "ollama", Label: "Ollama (" + modelName + ")", Status: "connecting"})
		if err := modelManager.Ping(); err != nil {
			p.Send(tui.StartupStatusMsg{ID: "ollama", Label: "Ollama (" + modelName + ")", Status: "failed", Detail: err.Error()})
			p.Send(tui.ErrorMsg{Msg: fmt.Sprintf("ollama: %v\nhint: is `ollama serve` running? is %q pulled?", err, modelName)})
			// Continue — non-fatal for TUI, user can see the error.
		} else {
			p.Send(tui.StartupStatusMsg{ID: "ollama", Label: "Ollama (" + modelName + ")", Status: "connected"})
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
				p.Send(tui.StartupStatusMsg{ID: "mcp:" + s.Name, Label: s.Name, Status: "connecting"})
				if initCtx.Err() != nil {
					return
				}
				toolCount, err := registry.ConnectServer(initCtx, s)
				if err != nil {
					p.Send(tui.StartupStatusMsg{ID: "mcp:" + s.Name, Label: s.Name, Status: "failed", Detail: err.Error()})
				} else {
					p.Send(tui.StartupStatusMsg{ID: "mcp:" + s.Name, Label: s.Name, Status: "connected", Detail: fmt.Sprintf("%d tools", toolCount)})
				}
			}(srv)
		}
		wg.Wait()

		if initCtx.Err() != nil {
			return
		}

		// 3. ICE setup.
		var iceEnabled bool
		var iceConversations int
		var iceSessionID string
		if cfg.ICE.Enabled {
			p.Send(tui.StartupStatusMsg{ID: "ice", Label: "ICE", Status: "connecting"})
			embedModel := cfg.ICE.EmbedModel
			if embedModel == "" {
				embedModel = cfg.Model.EmbedModel
			}
			iceEngine, err := ice.NewEngine(modelManager, memStore, ice.EngineConfig{
				EmbedModel: embedModel,
				StorePath:  cfg.ICE.StorePath,
				NumCtx:     cfg.Ollama.NumCtx,
			})
			if err != nil {
				p.Send(tui.StartupStatusMsg{ID: "ice", Label: "ICE", Status: "failed", Detail: err.Error()})
			} else {
				ag.SetICEEngine(iceEngine)
				iceEnabled = true
				iceConversations = iceEngine.Store().Count()
				iceSessionID = iceEngine.SessionID()
				p.Send(tui.StartupStatusMsg{ID: "ice", Label: "ICE", Status: "connected", Detail: fmt.Sprintf("%d conversations", iceConversations)})
			}
		}

		// 4. Load context and agent profile.
		if agentsDir != nil && agentsDir.GetGlobalInstructions() != "" {
			ag.SetLoadedContext(agentsDir.GetGlobalInstructions())
		}
		if data, err := os.ReadFile("AGENT.md"); err == nil {
			ag.AppendLoadedContext("\n\n" + string(data))
		}
		if cfg.AgentProfile != "" && agentsDir != nil {
			if profile := agentsDir.GetAgent(cfg.AgentProfile); profile != nil {
				if profile.SystemPrompt != "" {
					ag.AppendLoadedContext("\n\n" + profile.SystemPrompt)
				}
				for _, skillName := range profile.Skills {
					skillMgr.Activate(skillName)
				}
				ag.SetSkillContent(skillMgr.ActiveContent())
			}
		}

		// 5. Collect results and send InitCompleteMsg.
		var failedServers []tui.FailedServer
		for _, fs := range registry.FailedServers() {
			failedServers = append(failedServers, tui.FailedServer{
				Name:   fs.Name,
				Reason: fs.Reason,
			})
		}

		p.Send(tui.InitCompleteMsg{
			Model:            modelName,
			ModelList:        modelList,
			AgentProfile:     cfg.AgentProfile,
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

	if _, err := p.Run(); err != nil {
		log.Fatalf("tui: %v", err)
	}

	initCancel()
	<-initDone
}

// handleLogs implements the "logs" subcommand.
// With -f it execs tail -f on the latest log file; otherwise it lists recent sessions.
func handleLogs(args []string) {
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
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "following %s\n", latest)
		tailBin, err := exec.LookPath("tail")
		if err != nil {
			fmt.Fprintf(os.Stderr, "logs: tail not found: %v\n", err)
			os.Exit(1)
		}
		// Replace the process with tail -f.
		if err := syscall.Exec(tailBin, []string{"tail", "-f", latest}, os.Environ()); err != nil {
			fmt.Fprintf(os.Stderr, "logs: exec tail: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// List recent log sessions.
	entries, err := logging.ListLogs(20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logs: %v\n", err)
		os.Exit(1)
	}
	if len(entries) == 0 {
		fmt.Println("No log files found in", logging.LogDir())
		return
	}

	fmt.Printf("Recent sessions (%s):\n\n", logging.LogDir())
	for _, e := range entries {
		name := filepath.Base(e.Path)
		sizeKB := float64(e.Size) / 1024
		fmt.Printf("  %-30s  %s  %6.1f KB\n", name, e.ModTime.Format("2006-01-02 15:04:05"), sizeKB)
	}
	fmt.Printf("\nTip: run `local-agent logs -f` to follow the latest log.\n")
}
