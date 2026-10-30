package mcpserver

import (
	"fmt"
	"jakegodsall/knowledge-tree/src/domain"
	"jakegodsall/knowledge-tree/src/lookup"
	"strings"

	"github.com/google/uuid"
)

// snapshot is one tree loaded into memory with its tags and prerequisites, so
// an outline needs three queries rather than several per node.
type snapshot struct {
	tree  *domain.KnowledgeTree
	nodes map[uuid.UUID]*domain.Node
	// children are ordered by position; top-level nodes sit under uuid.Nil
	children map[uuid.UUID][]*domain.Node
	tags     map[uuid.UUID][]string
	requires map[uuid.UUID][]uuid.UUID
	// names labels prerequisites, which can live in other trees
	names map[uuid.UUID]string
	tally map[uuid.UUID][2]int
}

func (h *handlers) snapshot(tree *domain.KnowledgeTree) (*snapshot, error) {
	nodes, err := h.repos.Nodes.FindByTree(tree.ID)

	if err != nil {
		return nil, err
	}

	s := &snapshot{
		tree:     tree,
		nodes:    map[uuid.UUID]*domain.Node{},
		children: map[uuid.UUID][]*domain.Node{},
		tags:     map[uuid.UUID][]string{},
		requires: map[uuid.UUID][]uuid.UUID{},
		names:    map[uuid.UUID]string{},
		tally:    map[uuid.UUID][2]int{},
	}

	for _, n := range nodes {
		s.nodes[n.ID] = n
		s.names[n.ID] = n.Name
		parent := uuid.Nil

		if n.ParentID != nil {
			parent = *n.ParentID
		}

		s.children[parent] = append(s.children[parent], n)
	}

	tags, err := h.repos.Tags.GetAll()

	if err != nil {
		return nil, err
	}

	for _, t := range tags {
		if _, ok := s.nodes[t.NodeID]; ok {
			s.tags[t.NodeID] = append(s.tags[t.NodeID], t.Tag)
		}
	}

	prerequisites, err := h.repos.Prerequisites.GetAll()

	if err != nil {
		return nil, err
	}

	for _, p := range prerequisites {
		if _, ok := s.nodes[p.NodeID]; !ok {
			continue
		}

		s.requires[p.NodeID] = append(s.requires[p.NodeID], p.RequiresNodeID)

		if _, known := s.names[p.RequiresNodeID]; !known {
			if other, err := h.repos.Nodes.FindByID(p.RequiresNodeID); err == nil {
				s.names[other.ID] = other.Name
			}
		}
	}

	return s, nil
}

// counts returns how many of a node's descendants are complete, and how many
// there are.
func (s *snapshot) counts(id uuid.UUID) (int, int) {
	if t, ok := s.tally[id]; ok {
		return t[0], t[1]
	}

	done, total := 0, 0

	for _, child := range s.children[id] {
		total++

		if child.Status == domain.StatusComplete {
			done++
		}

		d, t := s.counts(child.ID)
		done += d
		total += t
	}

	s.tally[id] = [2]int{done, total}
	return done, total
}

func (s *snapshot) countAll() (int, int) {
	return s.counts(uuid.Nil)
}

func (s *snapshot) parentOf(id uuid.UUID) uuid.UUID {
	if n, ok := s.nodes[id]; ok && n.ParentID != nil {
		return *n.ParentID
	}

	return uuid.Nil
}

// path is "Tree › Branch › Node". uuid.Nil gives just the tree name.
func (s *snapshot) path(id uuid.UUID) string {
	parts := []string{}

	for id != uuid.Nil {
		n, ok := s.nodes[id]

		if !ok {
			break
		}

		parts = append([]string{n.Name}, parts...)
		id = s.parentOf(id)
	}

	return strings.Join(append([]string{s.tree.Name}, parts...), " › ")
}

// annotations are the " #tag (requires: …)" suffix for a node's line.
func (s *snapshot) annotations(id uuid.UUID) string {
	var b strings.Builder

	for _, tag := range s.tags[id] {
		b.WriteString(" #")
		b.WriteString(tag)
	}

	if required := s.requires[id]; len(required) > 0 {
		labels := []string{}

		for _, r := range required {
			labels = append(labels, fmt.Sprintf("%s [%s]", s.names[r], lookup.ShortID(r)))
		}

		b.WriteString(" (requires: ")
		b.WriteString(strings.Join(labels, ", "))
		b.WriteString(")")
	}

	return b.String()
}

type renderer struct {
	snap      *snapshot
	out       *strings.Builder
	maxDepth  int // -1 for unlimited
	lines     int
	truncated bool
}

func (r *renderer) render(parent uuid.UUID, indent string, depth int) {
	siblings := r.snap.children[parent]

	for i, node := range siblings {
		if r.lines >= maxLines {
			r.truncated = true
			return
		}

		branch, childIndent := "├── ", "│   "

		if i == len(siblings)-1 {
			branch, childIndent = "└── ", "    "
		}

		fmt.Fprintf(r.out, "%s%s%s %s [%s]", indent, branch, status(node), node.Name, lookup.ShortID(node.ID))

		hasChildren := len(r.snap.children[node.ID]) > 0
		expand := hasChildren && (r.maxDepth < 0 || depth < r.maxDepth)

		if hasChildren {
			done, total := r.snap.counts(node.ID)

			if expand {
				fmt.Fprintf(r.out, "  %d/%d", done, total)
			} else {
				fmt.Fprintf(r.out, "  %d/%d, collapsed", done, total)
			}
		}

		r.out.WriteString(r.snap.annotations(node.ID))
		r.out.WriteString("\n")
		r.lines++

		if expand {
			r.render(node.ID, indent+childIndent, depth+1)
		}
	}
}
