package repository

import (
	"jakegodsall/knowledge-tree/src/domain"

	"github.com/google/uuid"
)

type KnowledgeTreeRepository interface {
	GetAll() ([]*domain.KnowledgeTree, error)
	GetAllSorted() ([]*domain.KnowledgeTree, error)
	Create(tree *domain.KnowledgeTree) error
	FindByID(id uuid.UUID) (*domain.KnowledgeTree, error)
	FindByName(name string) (*domain.KnowledgeTree, error)
	// DeleteByID deletes the tree. The SQLite implementation also deletes the
	// tree's nodes.
	DeleteByID(id uuid.UUID) error
}
