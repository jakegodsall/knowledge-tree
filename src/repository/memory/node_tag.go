package memory

import (
	"fmt"
	"jakegodsall/knowledge-tree/src/domain"

	"github.com/google/uuid"
)

type NodeTagRepository struct {
	tags []*domain.NodeTag
}

func NewNodeTagRepository() *NodeTagRepository {
	return &NodeTagRepository{}
}

func (r *NodeTagRepository) Create(tag *domain.NodeTag) error {
	for _, t := range r.tags {
		if t.NodeID == tag.NodeID && t.Tag == tag.Tag {
			return fmt.Errorf("node %s is already tagged %s", tag.NodeID.String(), tag.Tag)
		}
	}

	r.tags = append(r.tags, tag)
	return nil
}

func (r *NodeTagRepository) GetAll() ([]*domain.NodeTag, error) {
	return append([]*domain.NodeTag{}, r.tags...), nil
}

func (r *NodeTagRepository) FindByNode(nodeID uuid.UUID) ([]*domain.NodeTag, error) {
	found := []*domain.NodeTag{}

	for _, t := range r.tags {
		if t.NodeID == nodeID {
			found = append(found, t)
		}
	}

	return found, nil
}

func (r *NodeTagRepository) FindByTag(tag string) ([]*domain.NodeTag, error) {
	tag = domain.NormaliseTag(tag)
	found := []*domain.NodeTag{}

	for _, t := range r.tags {
		if t.Tag == tag {
			found = append(found, t)
		}
	}

	return found, nil
}

func (r *NodeTagRepository) Delete(nodeID uuid.UUID, tag string) error {
	tag = domain.NormaliseTag(tag)
	idx := -1

	for i, t := range r.tags {
		if t.NodeID == nodeID && t.Tag == tag {
			idx = i
			break
		}
	}

	if idx == -1 {
		return fmt.Errorf("node %s is not tagged %s", nodeID.String(), tag)
	}

	r.tags = append(r.tags[:idx], r.tags[idx+1:]...)
	return nil
}
