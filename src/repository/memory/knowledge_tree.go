package memory

import (
	"fmt"
	"jakegodsall/knowledge-tree/src/domain"

	"github.com/google/uuid"
)

type KnowledgeTreeRepository struct {
	trees []*domain.KnowledgeTree
}

func NewKnowledgeTreeRepository() *KnowledgeTreeRepository {
	return &KnowledgeTreeRepository{}
}

func (r *KnowledgeTreeRepository) GetAll() ([]*domain.KnowledgeTree, error) {
	return r.trees, nil
}

func (r *KnowledgeTreeRepository) Create(tree *domain.KnowledgeTree) error {
	r.trees = append(r.trees, tree)
	return nil
}

func (r *KnowledgeTreeRepository) FindByID(id uuid.UUID) (*domain.KnowledgeTree, error) {
	for _, k := range r.trees {
		if k.ID == id {
			return k, nil
		}
	}

	return nil, fmt.Errorf("no knowledge tree found for id %s", id.String())
}

func (r *KnowledgeTreeRepository) FindByName(name string) (*domain.KnowledgeTree, error) {
	for _, k := range r.trees {
		if k.Name == name {
			return k, nil
		}
	}

	return nil, fmt.Errorf("no knowledge tree found with name %s", name)
}

func (r *KnowledgeTreeRepository) DeleteByID(id uuid.UUID) error {
	idx := -1

	for i, k := range r.trees {
		if k.ID == id {
			idx = i
			break
		}
	}

	if idx == -1 {
		return fmt.Errorf("no knowledge tree found for id %s", id.String())
	}

	r.trees = append(r.trees[:idx], r.trees[idx+1:]...)
	return nil
}
