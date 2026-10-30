package repository

import (
	"jakegodsall/knowledge-tree/src/domain"

	"github.com/google/uuid"
)

type NodeTagRepository interface {
	Create(tag *domain.NodeTag) error
	GetAll() ([]*domain.NodeTag, error)
	FindByNode(nodeID uuid.UUID) ([]*domain.NodeTag, error)
	FindByTag(tag string) ([]*domain.NodeTag, error)
	Delete(nodeID uuid.UUID, tag string) error
}
