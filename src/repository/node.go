package repository

import (
	"jakegodsall/knowledge-tree/src/domain"

	"github.com/google/uuid"
)

type NodeRepository interface {
	Create(node *domain.Node) error
	FindByID(id uuid.UUID) (*domain.Node, error)
	// FindByIDPrefix returns every node whose ID starts with prefix, so short
	// IDs can be used on the command line.
	FindByIDPrefix(prefix string) ([]*domain.Node, error)
	FindByTree(graphID uuid.UUID) ([]*domain.Node, error)
	FindRoots(graphID uuid.UUID) ([]*domain.Node, error)
	FindChildren(parentID uuid.UUID) ([]*domain.Node, error)
	// Search returns up to limit nodes whose name contains query, ignoring case.
	Search(query string, limit int) ([]*domain.Node, error)
	// InsertAt creates the node at a 0-based index among its siblings, moving
	// later siblings down one. An index past the end appends. It sets
	// node.Position.
	InsertAt(node *domain.Node, index int) error
	// Move puts the node, with its subtree, at a 0-based index under a new
	// parent in the same tree (nil for the top level). It fails if the new
	// parent is the node itself or one of its descendants.
	Move(id uuid.UUID, parentID *uuid.UUID, index int) error
	// PromoteToTree moves the node and its entire subtree into a new knowledge
	// tree, removing it from its current tree. The new tree record is created
	// atomically in the same transaction.
	PromoteToTree(nodeID uuid.UUID, newTree *domain.KnowledgeTree) error
	Update(node *domain.Node) error
	// DeleteByID deletes the node and all of its descendants, and closes the
	// gap among its siblings.
	DeleteByID(id uuid.UUID) error
}
