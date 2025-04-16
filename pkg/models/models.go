// pkg/models/models.go
package models

import (
	"fmt"
	"math/rand"
)

type Trie struct {
	Root *Node
}

type Node struct {
	Children map[rune]*Node
	Value    *Payload
}

// Suggestion holds individual suggestion data.
type Suggestion struct {
	Value    string  `json:"value"`    // Suggestion text/value.
	Weight   float64 `json:"weight"`   // Ranking weight.
	IconPath string  `json:"iconPath"` // Path to an icon.
	Command  string  `json:"command"`  // Associated command.
}

// PluginGroup groups suggestions from a specific plugin.
type PluginGroup struct {
	Plugin      string       `json:"plugin"`      // Plugin identifier.
	Suggestions []Suggestion `json:"suggestions"` // List of suggestions.
}

// Payload represents the complete message sent to the UI.
type Payload struct {
	Value  string        `json:"value"`  // Input string.
	Groups []PluginGroup `json:"groups"` // Grouped suggestions by plugin.
}

func NewTrie() *Trie {
	return &Trie{
		Root: &Node{
			Children: make(map[rune]*Node),
			Value:    nil,
		},
	}
}

func (t *Trie) Insert(key string, value *Payload) {
	current := t.Root
	for _, r := range key {
		if current.Children[r] == nil {
			current.Children[r] = &Node{
				Children: make(map[rune]*Node),
				Value:    nil,
			}
		}
		current = current.Children[r]
	}
	current.Value = value
}

func CreateTestTrie(words []string) *Trie {
	trie := NewTrie()

	pluginNames := []string{"alpha-plugin", "beta-plugin", "gamma-plugin"}

	for _, word := range words {
		for i := 1; i <= len(word); i++ {
			prefix := word[:i]

			groups := make([]PluginGroup, 0, len(pluginNames))
			for _, plugin := range pluginNames {
				suggestions := make([]Suggestion, 0, 5)
				for j := 1; j <= 5; j++ {
					suggestions = append(suggestions, Suggestion{
						Value:    fmt.Sprintf("%s-%s-suggestion-%d", prefix, plugin, j),
						Weight:   rand.Float64() * 10,
						IconPath: fmt.Sprintf("/icons/%s-%d.png", prefix, j),
						Command:  fmt.Sprintf("run --%s=%s%d", plugin, prefix, j),
					})
				}
				groups = append(groups, PluginGroup{
					Plugin:      plugin,
					Suggestions: suggestions,
				})
			}

			payload := &Payload{
				Value:  prefix,
				Groups: groups,
			}

			trie.Insert(prefix, payload)
		}
	}

	return trie
}
