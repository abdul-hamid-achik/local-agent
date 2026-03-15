package main

import "github.com/abdul-hamid-achik/local-agent/internal/config"

func newModelRouter(cfg *config.ModelConfig, useQwen bool) config.ModelRouter {
	if useQwen {
		return config.NewQwenModelRouter(cfg)
	}
	return config.NewRouter(cfg)
}
