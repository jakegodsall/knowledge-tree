package memory

import (
	"fmt"
	"jakegodsall/knowledge-tree/src/domain"
	"sort"
	"strings"

	"github.com/google/uuid"
)

type NodeRepository struct {
	nodes []*domain.Node
}

func NewNodeRepository() *NodeRepository {
	return &NodeRepository{}
}

func (r *NodeRepository) Create(node *domain.Node) error {
	r.nodes = append(r.nodes, node)
	return nil
}

func (r *NodeRepository) FindByID(id uuid.UUID) (*domain.Node, error) {
	for _, n := range r.nodes {
		if n.ID == id {
			return n, nil
		}
	}

	return nil, fmt.Errorf("no node found for id %s", id.String())
}

func (r *NodeRepository) FindByIDPrefix(prefix string) ([]*domain.Node, error) {
	prefix = strings.ToLower(prefix)
	found := []*domain.Node{}

	for _, n := range r.nodes {
		if strings.HasPrefix(n.ID.String(), prefix) {
			found = append(found, n)
		}
	}

	return found, nil
}

func (r *NodeRepository) FindByTree(graphID uuid.UUID) ([]*domain.Node, error) {
	return r.filter(func(n *domain.Node) bool {
		return n.GraphID == graphID
	}), nil
}

func (r *NodeRepository) FindRoots(graphID uuid.UUID) ([]*domain.Node, error) {
	return r.filter(func(n *domain.Node) bool {
		return n.GraphID == graphID && n.ParentID == nil
	}), nil
}

func (r *NodeRepository) FindChildren(parentID uuid.UUID) ([]*domain.Node, error) {
	return r.filter(func(n *domain.Node) bool {
		return n.ParentID != nil && *n.ParentID == parentID
	}), nil
}

func (r *NodeRepository) Search(query string, limit int) ([]*domain.Node, error) {
	query = strings.ToLower(query)
	found := []*domain.Node{}

	for _, n := range r.nodes {
		if strings.Contains(strings.ToLower(n.Name), query) {
			found = append(found, n)
		}
	}

	sort.SliceStable(found, func(i, j int) bool {
		if len(found[i].Name) != len(found[j].Name) {
			return len(found[i].Name) < len(found[j].Name)
		}

		return found[i].Name < found[j].Name
	})

	if len(found) > limit {
		found = found[:limit]
	}

	return found, nil
}

// filter returns matching nodes ordered by position, like the SQLite queries.
func (r *NodeRepository) filter(match func(*domain.Node) bool) []*domain.Node {
	found := []*domain.Node{}

	for _, n := range r.nodes {
		if match(n) {
			found = append(found, n)
		}
	}

	sort.SliceStable(found, func(i, j int) bool {
		return found[i].Position < found[j].Position
	})

	return found
}

func (r *NodeRepository) Update(node *domain.Node) error {
	for i, n := range r.nodes {
		if n.ID == node.ID {
			r.nodes[i] = node
			return nil
		}
	}

	return fmt.Errorf("no node found for id %s", node.ID.String())
}

func (r *NodeRepository) InsertAt(node *domain.Node, index int) error {
	siblings := r.siblings(node.GraphID, node.ParentID, uuid.Nil)
	node.Position = positionForIndex(siblings, index)

	for _, s := range siblings {
		if s.Position >= node.Position {
			s.Position++
		}
	}

	r.nodes = append(r.nodes, node)
	return nil
}

func (r *NodeRepository) Move(id uuid.UUID, parentID *uuid.UUID, index int) error {
	node, err := r.FindByID(id)

	if err != nil {
		return err
	}

	if parentID != nil {
		parent, err := r.FindByID(*parentID)

		if err != nil {
			return err
		}

		if parent.GraphID != node.GraphID {
			return fmt.Errorf("node %s can't move to another tree", id)
		}

		if r.subtree(id)[*parentID] {
			return fmt.Errorf("node %s can't move under itself or one of its descendants", id)
		}
	}

	r.closeGap(node)

	siblings := r.siblings(node.GraphID, parentID, id)
	position := positionForIndex(siblings, index)

	for _, s := range siblings {
		if s.Position >= position {
			s.Position++
		}
	}

	node.ParentID = parentID
	node.Position = position
	node.Touch()
	return nil
}

func (r *NodeRepository) PromoteToTree(nodeID uuid.UUID, newTree *domain.KnowledgeTree) error {
	return fmt.Errorf("PromoteToTree not implemented in memory repository")
}

func (r *NodeRepository) DemoteTree(treeID uuid.UUID, node *domain.Node, index int, newTree *domain.KnowledgeTree) error {
	return fmt.Errorf("DemoteTree not implemented in memory repository")
}

func (r *NodeRepository) DeleteByID(id uuid.UUID) error {
	node, err := r.FindByID(id)

	if err != nil {
		return err
	}

	subtree := r.subtree(id)
	remaining := []*domain.Node{}

	for _, n := range r.nodes {
		if !subtree[n.ID] {
			remaining = append(remaining, n)
		}
	}

	r.nodes = remaining
	r.closeGap(node)
	return nil
}

// subtree returns id and all of its descendants.
func (r *NodeRepository) subtree(id uuid.UUID) map[uuid.UUID]bool {
	subtree := map[uuid.UUID]bool{id: true}

	// keep sweeping until no more descendants are found
	for added := true; added; {
		added = false

		for _, n := range r.nodes {
			if n.ParentID != nil && subtree[*n.ParentID] && !subtree[n.ID] {
				subtree[n.ID] = true
				added = true
			}
		}
	}

	return subtree
}

// siblings returns the nodes under a parent ordered by position, leaving out
// exclude (uuid.Nil excludes nothing).
func (r *NodeRepository) siblings(graphID uuid.UUID, parentID *uuid.UUID, exclude uuid.UUID) []*domain.Node {
	return r.filter(func(n *domain.Node) bool {
		return n.GraphID == graphID && sameParent(n.ParentID, parentID) && n.ID != exclude
	})
}

// closeGap moves the node's later siblings up one once it has left.
func (r *NodeRepository) closeGap(node *domain.Node) {
	for _, s := range r.siblings(node.GraphID, node.ParentID, node.ID) {
		if s.Position > node.Position {
			s.Position--
		}
	}
}

func sameParent(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}

	return *a == *b
}

// positionForIndex converts a 0-based index among siblings into a position
// value. Indexes past the end append.
func positionForIndex(siblings []*domain.Node, index int) uint16 {
	if index < 0 {
		index = 0
	}

	if index < len(siblings) {
		return siblings[index].Position
	}

	if len(siblings) == 0 {
		return 0
	}

	return siblings[len(siblings)-1].Position + 1
}
