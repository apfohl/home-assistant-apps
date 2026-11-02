package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type mockClient struct {
	mu      sync.Mutex
	aliases map[string]string
}

func newMockClient() *mockClient {
	return &mockClient{aliases: map[string]string{"hello@example.com": "alice@example.com, bob@example.com"}}
}
func (m *mockClient) ListAliases(_ context.Context) ([]Alias, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Alias, 0, len(m.aliases))
	for a, g := range m.aliases {
		out = append(out, Alias{Address: a, Goto: g})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out, nil
}
func (m *mockClient) GetAlias(_ context.Context, a string) (*Alias, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.aliases[a]
	if !ok {
		return nil, fmt.Errorf("alias not found")
	}
	return &Alias{Address: a, Goto: g}, nil
}
func (m *mockClient) CreateAlias(_ context.Context, addresses, targets []string, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range addresses {
		if _, ok := m.aliases[a]; ok {
			return fmt.Errorf("alias already exists")
		}
	}
	for _, a := range addresses {
		m.aliases[a] = strings.Join(targets, ", ")
	}
	return nil
}
func (m *mockClient) EditAlias(_ context.Context, a string, targets []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.aliases[a]; !ok {
		return fmt.Errorf("alias not found")
	}
	m.aliases[a] = strings.Join(targets, ", ")
	return nil
}
func (m *mockClient) DeleteAlias(_ context.Context, a string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.aliases[a]; !ok {
		return fmt.Errorf("alias not found")
	}
	delete(m.aliases, a)
	return nil
}
