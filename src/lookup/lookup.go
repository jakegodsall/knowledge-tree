// Package lookup resolves the tree and node references people and agents type:
// tree names, full IDs, and short ID prefixes.
package lookup

import (
	"fmt"
	"jakegodsall/knowledge-tree/src/domain"
	"jakegodsall/knowledge-tree/src/repository"
	"strings"

	"github.com/google/uuid"
)

// MinPrefixLength stops very short prefixes matching half the database.
const MinPrefixLength = 4

// Tree accepts a full tree ID or a tree name.
func Tree(trees repository.KnowledgeTreeRepository, arg string) (*domain.KnowledgeTree, error) {
	if id, err := uuid.Parse(arg); err == nil {
		return trees.FindByID(id)
	}

	return trees.FindByName(arg)
}

// Node accepts a full node ID or a unique prefix of one.
func Node(nodes repository.NodeRepository, arg string) (*domain.Node, error) {
	if id, err := uuid.Parse(arg); err == nil {
		return nodes.FindByID(id)
	}

	if len(arg) < MinPrefixLength {
		return nil, fmt.Errorf("node ID %q is too short, use at least %d characters", arg, MinPrefixLength)
	}

	matches, err := nodes.FindByIDPrefix(arg)

	if err != nil {
		return nil, err
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no node found for id %s", arg)
	case 1:
		return matches[0], nil
	}

	names := []string{}

	for _, m := range matches {
		names = append(names, fmt.Sprintf("%s [%s]", m.Name, m.ID.String()))
	}

	return nil, fmt.Errorf("node ID %s is ambiguous, it matches:\n  %s", arg, strings.Join(names, "\n  "))
}

// ShortID is the 8-character form of an ID shown to people and agents.
func ShortID(id uuid.UUID) string {
	return id.String()[:8]
}
