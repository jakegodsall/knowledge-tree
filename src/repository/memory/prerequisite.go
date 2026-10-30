package memory

import (
	"fmt"
	"jakegodsall/knowledge-tree/src/domain"

	"github.com/google/uuid"
)

type PrerequisiteRepository struct {
	prerequisites []*domain.Prerequisite
}

func NewPrerequisiteRepository() *PrerequisiteRepository {
	return &PrerequisiteRepository{}
}

func (r *PrerequisiteRepository) Create(prerequisite *domain.Prerequisite) error {
	for _, p := range r.prerequisites {
		if p.NodeID == prerequisite.NodeID && p.RequiresNodeID == prerequisite.RequiresNodeID {
			return fmt.Errorf("node %s already requires node %s", prerequisite.NodeID.String(), prerequisite.RequiresNodeID.String())
		}
	}

	if r.requires(prerequisite.RequiresNodeID, prerequisite.NodeID) {
		return fmt.Errorf("node %s requires node %s: %w", prerequisite.NodeID.String(), prerequisite.RequiresNodeID.String(), domain.ErrPrerequisiteCycle)
	}

	r.prerequisites = append(r.prerequisites, prerequisite)
	return nil
}

func (r *PrerequisiteRepository) GetAll() ([]*domain.Prerequisite, error) {
	return append([]*domain.Prerequisite{}, r.prerequisites...), nil
}

func (r *PrerequisiteRepository) FindByNode(nodeID uuid.UUID) ([]*domain.Prerequisite, error) {
	found := []*domain.Prerequisite{}

	for _, p := range r.prerequisites {
		if p.NodeID == nodeID {
			found = append(found, p)
		}
	}

	return found, nil
}

func (r *PrerequisiteRepository) FindDependents(nodeID uuid.UUID) ([]*domain.Prerequisite, error) {
	found := []*domain.Prerequisite{}

	for _, p := range r.prerequisites {
		if p.RequiresNodeID == nodeID {
			found = append(found, p)
		}
	}

	return found, nil
}

func (r *PrerequisiteRepository) Delete(nodeID uuid.UUID, requiresNodeID uuid.UUID) error {
	idx := -1

	for i, p := range r.prerequisites {
		if p.NodeID == nodeID && p.RequiresNodeID == requiresNodeID {
			idx = i
			break
		}
	}

	if idx == -1 {
		return fmt.Errorf("node %s does not require node %s", nodeID.String(), requiresNodeID.String())
	}

	r.prerequisites = append(r.prerequisites[:idx], r.prerequisites[idx+1:]...)
	return nil
}

// requires reports whether from depends on target, directly or transitively.
func (r *PrerequisiteRepository) requires(from uuid.UUID, target uuid.UUID) bool {
	visited := map[uuid.UUID]bool{}
	stack := []uuid.UUID{from}

	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if current == target {
			return true
		}

		if visited[current] {
			continue
		}

		visited[current] = true

		for _, p := range r.prerequisites {
			if p.NodeID == current {
				stack = append(stack, p.RequiresNodeID)
			}
		}
	}

	return false
}
