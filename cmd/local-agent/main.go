package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/abdulachik/local-agent/internal/agent"
	"github.com/abdulachik/local-agent/internal/command"
	"github.com/abdulachik/local-agent/internal/config"
	"github.com/abdulachik/local-agent/internal/ice"
	"github.com/abdulachik/local-agent/internal/llm"
	"github.com/abdulachik/local-agent/internal/logging"
	"github.com/abdulachik/local-agent/internal/mcp"
	"github.com/abdulachik/local-agent/internal/memory"
	"github.com/abdulachik/local-agent/internal/skill"
	"github.com/abdulachik/local-agent/internal/tui"
)

func main() {
	modelFlag := flag.String("model", "", "override Ollama model")
	agentProfileFlag := flag.String("agent", "", "override agent profile")
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

	cmdReg := command.NewRegistry()
	command.RegisterBuiltins(cmdReg)

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
