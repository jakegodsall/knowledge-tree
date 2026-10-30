package repository

import (
	"jakegodsall/knowledge-tree/src/domain"

	"github.com/google/uuid"
)

type PrerequisiteRepository interface {
	Create(prerequisite *domain.Prerequisite) error
	GetAll() ([]*domain.Prerequisite, error)
	// FindByNode returns the prerequisites the node requires.
	FindByNode(nodeID uuid.UUID) ([]*domain.Prerequisite, error)
	// FindDependents returns the prerequisites that require the node.
	FindDependents(nodeID uuid.UUID) ([]*domain.Prerequisite, error)
	Delete(nodeID uuid.UUID, requiresNodeID uuid.UUID) error
}
