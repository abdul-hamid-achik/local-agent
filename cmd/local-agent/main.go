package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/abdulachik/local-agent/internal/agent"
	"github.com/abdulachik/local-agent/internal/command"
	"github.com/abdulachik/local-agent/internal/config"
	"github.com/abdulachik/local-agent/internal/ice"
	"github.com/abdulachik/local-agent/internal/llm"
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

	fmt.Fprintf(os.Stderr, "connecting to Ollama (%s)...\n", modelName)

	modelManager := llm.NewModelManager(cfg.Ollama.BaseURL, cfg.Ollama.NumCtx)
	modelManager.SetCurrentModel(modelName)
	if err := modelManager.Ping(); err != nil {
		log.Fatalf("ollama: %v\nhint: is `ollama serve` running? is %q pulled?",
			err, modelName)
	}

	var servers []config.ServerConfig
	if len(cfg.Servers) > 0 {
		servers = cfg.Servers
	} else if agentsDir != nil && agentsDir.HasMCP() {
		servers = agentsDir.GetMCPServers()
	}

	registry := mcp.NewRegistry()
	defer registry.Close()

	ctx := context.Background()
	registry.ConnectAll(ctx, servers, func(msg string) {
		fmt.Fprintf(os.Stderr, "  %s\n", msg)
	})

	ag := agent.New(modelManager, registry, cfg.Ollama.NumCtx)
	ag.SetRouter(router)
	defer ag.Close()

	memStore := memory.NewStore("")
	ag.SetMemoryStore(memStore)
	if memStore.Count() > 0 {
		fmt.Fprintf(os.Stderr, "  loaded %d memories\n", memStore.Count())
	}

	var iceEnabled bool
	var iceConversations int
	var iceSessionID string
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
			fmt.Fprintf(os.Stderr, "  ice: %v (disabled)\n", err)
		} else {
			ag.SetICEEngine(iceEngine)
			defer iceEngine.Flush()
			iceEnabled = true
			iceConversations = iceEngine.Store().Count()
			iceSessionID = iceEngine.SessionID()
			fmt.Fprintf(os.Stderr, "  ICE enabled (%d conversations stored)\n", iceConversations)
		}
	}

	skillDirs := []string{cfg.SkillsDir}
	if agentsDir != nil && len(agentsDir.Skills) > 0 {
		for _, s := range agentsDir.Skills {
			if s.Path != "" {
				// s.Path is the full path to SKILL.md, we need the parent dir
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
	}

	if agentsDir != nil && agentsDir.GetGlobalInstructions() != "" {
		ag.SetLoadedContext(agentsDir.GetGlobalInstructions())
		fmt.Fprintf(os.Stderr, "  loaded ~/.agents/agents.md\n")
	}

	if data, err := os.ReadFile("AGENT.md"); err == nil {
		ag.AppendLoadedContext("\n\n" + string(data))
		fmt.Fprintf(os.Stderr, "  loaded AGENT.md (%d bytes)\n", len(data))
	}

	if cfg.AgentProfile != "" && agentsDir != nil {
		if profile := agentsDir.GetAgent(cfg.AgentProfile); profile != nil {
			if profile.SystemPrompt != "" {
				ag.AppendLoadedContext("\n\n" + profile.SystemPrompt)
			}
			for _, skillName := range profile.Skills {
				if err := skillMgr.Activate(skillName); err == nil {
					fmt.Fprintf(os.Stderr, "  activated skill: %s\n", skillName)
				}
			}
			ag.SetSkillContent(skillMgr.ActiveContent())
			fmt.Fprintf(os.Stderr, "  loaded agent profile: %s\n", cfg.AgentProfile)
		}
	}

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

	completer := tui.NewCompleter(cmdReg, modelList, skillMgr.Names(), agentList)

	m := tui.New(ag, cmdReg, skillMgr, completer)
	p := tea.NewProgram(m)
	m.SetProgram(p)

	var failedServers []tui.FailedServer
	for _, fs := range registry.FailedServers() {
		failedServers = append(failedServers, tui.FailedServer{
			Name:   fs.Name,
			Reason: fs.Reason,
		})
	}

	go func() {
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
}
