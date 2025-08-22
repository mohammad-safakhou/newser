package main

import (
	"context"
	"sync"
)

// ==========================================================
// Agent Registry
// ==========================================================

type Agent struct {
	Name   string
	Client MCPClient
	Tools  map[string]MCPTool
	Can    map[string]bool
}

type AgentRegistry struct {
	mu     sync.RWMutex
	agents map[string]*Agent
}

func NewAgentRegistry() *AgentRegistry { return &AgentRegistry{agents: map[string]*Agent{}} }
func (r *AgentRegistry) Add(a *Agent)  { r.mu.Lock(); r.agents[a.Name] = a; r.mu.Unlock() }
func (r *AgentRegistry) All() []*Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Agent, 0, len(r.agents))
	for _, a := range r.agents {
		out = append(out, a)
	}
	return out
}
func (r *AgentRegistry) FindAgentFor(cap string) *Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, a := range r.agents {
		if a.Can[cap] {
			return a
		}
	}
	return nil
}

func primeAgent(ctx context.Context, a *Agent) error {
	tools, err := a.Client.ListTools(ctx)
	if err != nil {
		return err
	}
	a.Tools = map[string]MCPTool{}
	a.Can = map[string]bool{}
	for _, t := range tools {
		a.Tools[t.Name] = t
	}
	for capName, candidates := range CapabilityTools {
		for _, tool := range candidates {
			if _, ok := a.Tools[tool]; ok {
				a.Can[capName] = true
				break
			}
		}
	}
	return nil
}
