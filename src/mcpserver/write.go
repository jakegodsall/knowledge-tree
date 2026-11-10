package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"jakegodsall/knowledge-tree/src/domain"
	"jakegodsall/knowledge-tree/src/lookup"
	"math"
	"strings"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (h *handlers) addWriteTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "add_node",
		Description: "Add a topic to a tree. Place it with parent (add under that node), after (add right after that sibling), " +
			"or tree alone for a top-level topic; position picks a 0-based slot among the siblings instead of the end. " +
			"Search first so you don't add a duplicate. Returns the parent's updated children.",
		Annotations: writeTool(false),
	}, h.addNode)

	mcp.AddTool(server, &mcp.Tool{
		Name: "move_node",
		Description: "Move a topic, with everything beneath it, within its tree: under a new parent, after a sibling, to the top level, " +
			"or to a new position among its current siblings. Use it to restructure, e.g. to group existing topics under a new one. " +
			"Returns the new parent's updated children.",
		Annotations: writeTool(false),
	}, h.moveNode)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "rename_node",
		Description: "Rename a topic.",
		Annotations: writeTool(false),
	}, h.renameNode)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "set_status",
		Description: "Mark a topic complete (the user has studied it) or incomplete (to undo a mistaken complete).",
		Annotations: writeTool(false),
	}, h.setStatus)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "promote_to_tree",
		Description: "Promote a topic and all of its descendants to their own top-level tree, removing it from its current tree. The new tree takes the topic's name unless you supply a different one.",
		Annotations: writeTool(false),
	}, h.promoteToTree)

	mcp.AddTool(server, &mcp.Tool{
		Name: "demote_tree",
		Description: "The reverse of promote_to_tree: turn a whole tree into a topic inside another tree. A new topic named after the tree " +
			"(or name) is added under parent, or at the top level of into_tree, and the tree's top-level topics become its children. " +
			"into_tree is created if no tree has that name. The demoted tree is removed; its topics keep their IDs, status, tags and prerequisites.",
		Annotations: writeTool(false),
	}, h.demoteTree)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_node",
		Description: "Delete a topic and everything beneath it, including their tags and prerequisites. This can't be undone; confirm with the user first.",
		Annotations: writeTool(true),
	}, h.deleteNode)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "add_prerequisite",
		Description: "Record that node should be learned after requires. They can be in different trees. Rejected if it would create a loop.",
		Annotations: writeTool(false),
	}, h.addPrerequisite)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "remove_prerequisite",
		Description: "Remove a prerequisite between two topics.",
		Annotations: writeTool(false),
	}, h.removePrerequisite)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tag_node",
		Description: "Tag a topic, e.g. sap-c02 to mark it as exam material. A tag on a branch applies to everything beneath it. Tags are lowercased and spaces become hyphens.",
		Annotations: writeTool(false),
	}, h.tagNode)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "untag_node",
		Description: "Remove a tag from a topic.",
		Annotations: writeTool(false),
	}, h.untagNode)
}

func writeTool(destructive bool) *mcp.ToolAnnotations {
	closedWorld := false
	return &mcp.ToolAnnotations{DestructiveHint: &destructive, OpenWorldHint: &closedWorld}
}

// placement is where a node goes: a parent (nil for the top level) in a tree,
// and a 0-based index among that parent's children.
type placement struct {
	tree   *domain.KnowledgeTree
	parent *domain.Node
	index  int
}

func (p *placement) parentID() *uuid.UUID {
	if p.parent == nil {
		return nil
	}

	return &p.parent.ID
}

// placementArgs are the ways add_node and move_node can say where a node goes.
type placementArgs struct {
	parent   string
	after    string
	position *int
	tree     *domain.KnowledgeTree // used when neither parent nor after is given
	exclude  uuid.UUID             // the node being moved, left out of sibling counts
}

func (h *handlers) place(args placementArgs) (*placement, error) {
	if args.after != "" && args.position != nil {
		return nil, fmt.Errorf("give after or position, not both")
	}

	p := &placement{tree: args.tree, index: math.MaxInt32}

	if args.parent != "" {
		parent, err := lookup.Node(h.repos.Nodes, args.parent)

		if err != nil {
			return nil, err
		}

		p.parent = parent
	}

	if args.after != "" {
		after, err := lookup.Node(h.repos.Nodes, args.after)

		if err != nil {
			return nil, err
		}

		if after.ID == args.exclude {
			return nil, fmt.Errorf("a node can't be placed after itself")
		}

		var afterParent *domain.Node

		if after.ParentID != nil {
			if afterParent, err = h.repos.Nodes.FindByID(*after.ParentID); err != nil {
				return nil, err
			}
		}

		if p.parent != nil && (afterParent == nil || afterParent.ID != p.parent.ID) {
			return nil, fmt.Errorf("%s [%s] is not a child of %s [%s]", after.Name, lookup.ShortID(after.ID), p.parent.Name, lookup.ShortID(p.parent.ID))
		}

		p.parent = afterParent

		if p.tree, err = h.repos.Trees.FindByID(after.GraphID); err != nil {
			return nil, err
		}

		siblings, err := h.siblings(p.tree.ID, p.parentID(), args.exclude)

		if err != nil {
			return nil, err
		}

		for i, s := range siblings {
			if s.ID == after.ID {
				p.index = i + 1
			}
		}
	}

	if p.parent != nil {
		tree, err := h.repos.Trees.FindByID(p.parent.GraphID)

		if err != nil {
			return nil, err
		}

		if p.tree != nil && p.tree.ID != tree.ID {
			return nil, fmt.Errorf("%s [%s] is in tree %s, not %s", p.parent.Name, lookup.ShortID(p.parent.ID), tree.Name, p.tree.Name)
		}

		p.tree = tree
	}

	if p.tree == nil {
		return nil, fmt.Errorf("say where the node goes: give parent, after, or tree for a top-level topic")
	}

	if args.position != nil {
		p.index = *args.position
	}

	return p, nil
}

func (h *handlers) siblings(graphID uuid.UUID, parentID *uuid.UUID, exclude uuid.UUID) ([]*domain.Node, error) {
	var nodes []*domain.Node
	var err error

	if parentID == nil {
		nodes, err = h.repos.Nodes.FindRoots(graphID)
	} else {
		nodes, err = h.repos.Nodes.FindChildren(*parentID)
	}

	if err != nil {
		return nil, err
	}

	kept := []*domain.Node{}

	for _, n := range nodes {
		if n.ID != exclude {
			kept = append(kept, n)
		}
	}

	return kept, nil
}

// placed reports a node's new home with the parent's children around it.
func (h *handlers) placed(verb string, node *domain.Node, p *placement) (*mcp.CallToolResult, any, error) {
	outline, err := h.outline(p.tree, p.parent, 1)

	if err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("%s %s [%s]\n\n%s", verb, node.Name, lookup.ShortID(node.ID), outline)), nil, nil
}

type addNodeInput struct {
	Name     string `json:"name" jsonschema:"the new topic's name"`
	Parent   string `json:"parent,omitempty" jsonschema:"ID of the node to add the topic under"`
	After    string `json:"after,omitempty" jsonschema:"ID of a sibling to add the topic straight after. Its parent is used if parent is omitted."`
	Tree     string `json:"tree,omitempty" jsonschema:"tree name or ID. Only needed for a top-level topic."`
	Position *int   `json:"position,omitempty" jsonschema:"0-based slot among the siblings, 0 being first. Omit to add at the end."`
}

func (h *handlers) addNode(ctx context.Context, req *mcp.CallToolRequest, in addNodeInput) (*mcp.CallToolResult, any, error) {
	name := strings.TrimSpace(in.Name)

	if name == "" {
		return nil, nil, fmt.Errorf("name cannot be empty")
	}

	args := placementArgs{parent: in.Parent, after: in.After, position: in.Position}

	if in.Tree != "" {
		tree, err := lookup.Tree(h.repos.Trees, in.Tree)

		if err != nil {
			return nil, nil, err
		}

		args.tree = tree
	}

	p, err := h.place(args)

	if err != nil {
		return nil, nil, err
	}

	node := domain.NewNode(p.tree.ID, p.parentID(), name, 0)

	if err := h.repos.Nodes.InsertAt(node, p.index); err != nil {
		return nil, nil, err
	}

	return h.placed("added", node, p)
}

type moveNodeInput struct {
	Node     string `json:"node" jsonschema:"ID of the topic to move"`
	Parent   string `json:"parent,omitempty" jsonschema:"ID of the new parent"`
	After    string `json:"after,omitempty" jsonschema:"ID of a sibling to move the topic straight after. Its parent is used if parent is omitted."`
	TopLevel bool   `json:"top_level,omitempty" jsonschema:"move the topic to the top level of its tree"`
	Position *int   `json:"position,omitempty" jsonschema:"0-based slot among the new siblings, 0 being first. Omit to move to the end. With nothing else given, reorders the topic within its current parent."`
}

func (h *handlers) moveNode(ctx context.Context, req *mcp.CallToolRequest, in moveNodeInput) (*mcp.CallToolResult, any, error) {
	node, err := lookup.Node(h.repos.Nodes, in.Node)

	if err != nil {
		return nil, nil, err
	}

	if in.TopLevel && (in.Parent != "" || in.After != "") {
		return nil, nil, fmt.Errorf("top_level can't be combined with parent or after")
	}

	tree, err := h.repos.Trees.FindByID(node.GraphID)

	if err != nil {
		return nil, nil, err
	}

	args := placementArgs{parent: in.Parent, after: in.After, position: in.Position, tree: tree, exclude: node.ID}

	// with no destination, reorder within the current parent
	if in.Parent == "" && in.After == "" && !in.TopLevel {
		if in.Position == nil {
			return nil, nil, fmt.Errorf("say where to move it: parent, after, top_level or position")
		}

		if node.ParentID != nil {
			args.parent = node.ParentID.String()
		}
	}

	p, err := h.place(args)

	if err != nil {
		return nil, nil, err
	}

	if err := h.repos.Nodes.Move(node.ID, p.parentID(), p.index); err != nil {
		return nil, nil, err
	}

	return h.placed("moved", node, p)
}

type renameNodeInput struct {
	Node string `json:"node" jsonschema:"ID of the topic to rename"`
	Name string `json:"name" jsonschema:"the new name"`
}

func (h *handlers) renameNode(ctx context.Context, req *mcp.CallToolRequest, in renameNodeInput) (*mcp.CallToolResult, any, error) {
	name := strings.TrimSpace(in.Name)

	if name == "" {
		return nil, nil, fmt.Errorf("name cannot be empty")
	}

	node, err := lookup.Node(h.repos.Nodes, in.Node)

	if err != nil {
		return nil, nil, err
	}

	old := node.Name
	node.Rename(name)

	if err := h.repos.Nodes.Update(node); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("renamed %s to %s [%s]", old, node.Name, lookup.ShortID(node.ID))), nil, nil
}

type setStatusInput struct {
	Node   string `json:"node" jsonschema:"ID of the topic"`
	Status string `json:"status" jsonschema:"complete or incomplete"`
}

func (h *handlers) setStatus(ctx context.Context, req *mcp.CallToolRequest, in setStatusInput) (*mcp.CallToolResult, any, error) {
	node, err := lookup.Node(h.repos.Nodes, in.Node)

	if err != nil {
		return nil, nil, err
	}

	switch domain.Status(strings.ToLower(strings.TrimSpace(in.Status))) {
	case domain.StatusComplete:
		node.Complete()
	case domain.StatusIncomplete:
		node.Reopen()
	default:
		return nil, nil, fmt.Errorf("status must be complete or incomplete, not %q", in.Status)
	}

	if err := h.repos.Nodes.Update(node); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("%s %s [%s] is now %s", status(node), node.Name, lookup.ShortID(node.ID), node.Status)), nil, nil
}

type nodeInput struct {
	Node string `json:"node" jsonschema:"ID of the topic"`
}

func (h *handlers) deleteNode(ctx context.Context, req *mcp.CallToolRequest, in nodeInput) (*mcp.CallToolResult, any, error) {
	node, err := lookup.Node(h.repos.Nodes, in.Node)

	if err != nil {
		return nil, nil, err
	}

	tree, err := h.repos.Trees.FindByID(node.GraphID)

	if err != nil {
		return nil, nil, err
	}

	snap, err := h.snapshot(tree)

	if err != nil {
		return nil, nil, err
	}

	path := snap.path(node.ID)
	_, descendants := snap.counts(node.ID)

	if err := h.repos.Nodes.DeleteByID(node.ID); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("deleted %s [%s] and %d topics beneath it", path, lookup.ShortID(node.ID), descendants)), nil, nil
}

type prerequisiteInput struct {
	Node     string `json:"node" jsonschema:"ID of the topic to learn later"`
	Requires string `json:"requires" jsonschema:"ID of the topic to learn first"`
}

func (h *handlers) prerequisitePair(in prerequisiteInput) (*domain.Node, *domain.Node, error) {
	node, err := lookup.Node(h.repos.Nodes, in.Node)

	if err != nil {
		return nil, nil, err
	}

	required, err := lookup.Node(h.repos.Nodes, in.Requires)

	if err != nil {
		return nil, nil, err
	}

	return node, required, nil
}

func (h *handlers) addPrerequisite(ctx context.Context, req *mcp.CallToolRequest, in prerequisiteInput) (*mcp.CallToolResult, any, error) {
	node, required, err := h.prerequisitePair(in)

	if err != nil {
		return nil, nil, err
	}

	prerequisite, err := domain.NewPrerequisite(node.ID, required.ID)

	if err != nil {
		return nil, nil, err
	}

	err = h.repos.Prerequisites.Create(prerequisite)

	if errors.Is(err, domain.ErrPrerequisiteCycle) {
		return nil, nil, fmt.Errorf("%s already depends on %s, so it can't also be required by it", required.Name, node.Name)
	}

	if err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("%s [%s] now requires %s [%s]", node.Name, lookup.ShortID(node.ID), required.Name, lookup.ShortID(required.ID))), nil, nil
}

func (h *handlers) removePrerequisite(ctx context.Context, req *mcp.CallToolRequest, in prerequisiteInput) (*mcp.CallToolResult, any, error) {
	node, required, err := h.prerequisitePair(in)

	if err != nil {
		return nil, nil, err
	}

	if err := h.repos.Prerequisites.Delete(node.ID, required.ID); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("%s [%s] no longer requires %s [%s]", node.Name, lookup.ShortID(node.ID), required.Name, lookup.ShortID(required.ID))), nil, nil
}

type promoteToTreeInput struct {
	Node string `json:"node" jsonschema:"ID of the topic to promote to its own tree"`
	Name string `json:"name,omitempty" jsonschema:"name for the new tree; defaults to the topic's own name"`
}

func (h *handlers) promoteToTree(ctx context.Context, req *mcp.CallToolRequest, in promoteToTreeInput) (*mcp.CallToolResult, any, error) {
	node, err := lookup.Node(h.repos.Nodes, in.Node)
	if err != nil {
		return nil, nil, err
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = node.Name
	}

	newTree := domain.NewKnowledgeTree(name)

	if err := h.repos.Nodes.PromoteToTree(node.ID, newTree); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("promoted %s [%s] to its own tree %q [%s]", node.Name, lookup.ShortID(node.ID), newTree.Name, lookup.ShortID(newTree.ID))), nil, nil
}

type demoteTreeInput struct {
	Tree     string `json:"tree" jsonschema:"name or ID of the tree to demote"`
	Parent   string `json:"parent,omitempty" jsonschema:"ID of the node to put the tree under"`
	IntoTree string `json:"into_tree,omitempty" jsonschema:"name or ID of the tree to put it at the top level of; a tree with this name is created if none exists"`
	Name     string `json:"name,omitempty" jsonschema:"name for the new topic; defaults to the tree's name"`
	Position *int   `json:"position,omitempty" jsonschema:"0-based slot among the new siblings, 0 being first. Omit to add at the end."`
}

func (h *handlers) demoteTree(ctx context.Context, req *mcp.CallToolRequest, in demoteTreeInput) (*mcp.CallToolResult, any, error) {
	if (in.Parent == "") == (in.IntoTree == "") {
		return nil, nil, fmt.Errorf("give exactly one of parent or into_tree")
	}

	source, err := lookup.Tree(h.repos.Trees, in.Tree)
	if err != nil {
		return nil, nil, err
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = source.Name
	}

	p := &placement{index: math.MaxInt32}
	var newTree *domain.KnowledgeTree

	if in.Parent != "" {
		if p.parent, err = lookup.Node(h.repos.Nodes, in.Parent); err != nil {
			return nil, nil, err
		}
		if p.tree, err = h.repos.Trees.FindByID(p.parent.GraphID); err != nil {
			return nil, nil, err
		}
	} else {
		if p.tree, err = h.findTree(in.IntoTree); err != nil {
			return nil, nil, err
		}
		if p.tree == nil {
			newTree = domain.NewKnowledgeTree(strings.TrimSpace(in.IntoTree))
			p.tree = newTree
		}
	}

	if p.tree.ID == source.ID {
		return nil, nil, fmt.Errorf("%s can't be demoted into itself", source.Name)
	}

	if in.Position != nil {
		p.index = *in.Position
	}

	node := domain.NewNode(p.tree.ID, p.parentID(), name, 0)

	if err := h.repos.Nodes.DemoteTree(source.ID, node, p.index, newTree); err != nil {
		return nil, nil, err
	}

	verb := fmt.Sprintf("demoted tree %s to", source.Name)
	if newTree != nil {
		verb = fmt.Sprintf("created tree %s [%s] and demoted tree %s to", newTree.Name, lookup.ShortID(newTree.ID), source.Name)
	}

	return h.placed(verb, node, p)
}

// findTree resolves a tree name or ID, returning nil if no tree has that name.
func (h *handlers) findTree(arg string) (*domain.KnowledgeTree, error) {
	arg = strings.TrimSpace(arg)

	if _, err := uuid.Parse(arg); err == nil {
		return lookup.Tree(h.repos.Trees, arg)
	}

	trees, err := h.repos.Trees.GetAll()
	if err != nil {
		return nil, err
	}

	for _, t := range trees {
		if t.Name == arg {
			return t, nil
		}
	}

	return nil, nil
}

type tagInput struct {
	Node string `json:"node" jsonschema:"ID of the topic"`
	Tag  string `json:"tag" jsonschema:"the tag, e.g. sap-c02"`
}

func (h *handlers) tagNode(ctx context.Context, req *mcp.CallToolRequest, in tagInput) (*mcp.CallToolResult, any, error) {
	node, err := lookup.Node(h.repos.Nodes, in.Node)

	if err != nil {
		return nil, nil, err
	}

	tag, err := domain.NewNodeTag(node.ID, in.Tag)

	if err != nil {
		return nil, nil, err
	}

	if err := h.repos.Tags.Create(tag); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("tagged %s [%s] #%s", node.Name, lookup.ShortID(node.ID), tag.Tag)), nil, nil
}

func (h *handlers) untagNode(ctx context.Context, req *mcp.CallToolRequest, in tagInput) (*mcp.CallToolResult, any, error) {
	node, err := lookup.Node(h.repos.Nodes, in.Node)

	if err != nil {
		return nil, nil, err
	}

	if err := h.repos.Tags.Delete(node.ID, in.Tag); err != nil {
		return nil, nil, err
	}

	return text(fmt.Sprintf("removed #%s from %s [%s]", domain.NormaliseTag(in.Tag), node.Name, lookup.ShortID(node.ID))), nil, nil
}
