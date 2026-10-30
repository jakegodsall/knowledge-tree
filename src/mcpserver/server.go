// Package mcpserver exposes knowledge trees to agents over the Model Context
// Protocol. Tools return indented text outlines rather than JSON: they're
// easier for a model to read and use far fewer tokens on large trees.
package mcpserver

import (
	"context"
	"fmt"
	"jakegodsall/knowledge-tree/src/domain"
	"jakegodsall/knowledge-tree/src/lookup"
	"jakegodsall/knowledge-tree/src/repository"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const version = "0.1.0"

const instructions = `ktree holds the user's personal knowledge trees: one tree per subject (AWS, Golang, Databases, ...), each a hierarchy of topics they are learning.

Conventions:
- ○ is a topic not yet complete, ✓ is complete. Branches show "done/total" for everything beneath them.
- IDs in [brackets] are 8-character prefixes of node IDs. Pass them to other tools as-is.
- "requires" lists topics the user wants learned first. #tags scope topics, e.g. #sap-c02 for an exam. Both are hints from the user; use your own judgement where they're missing.

Large trees run to 1,000+ topics, so explore progressively: list_trees, then get_tree at the default depth, then get_tree with node=<id> to expand only the branch you need. Use search_nodes to find where a topic already lives before suggesting a new one.`

// Repositories are the stores the server reads from.
type Repositories struct {
	Trees         repository.KnowledgeTreeRepository
	Nodes         repository.NodeRepository
	Prerequisites repository.PrerequisiteRepository
	Tags          repository.NodeTagRepository
}

func New(repos Repositories) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "ktree", Version: version},
		&mcp.ServerOptions{Instructions: instructions},
	)

	h := &handlers{repos: repos}
	closedWorld := false
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_trees",
		Description: "List the user's knowledge trees with how many topics each has and how many are complete.",
		Annotations: readOnly,
	}, h.listTrees)

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_tree",
		Description: "Show a tree, or one branch of it, as an indented outline with each topic's status, ID, tags and prerequisites. " +
			"Branches deeper than the requested depth are collapsed with their done/total counts; expand one by calling again with node=<id>.",
		Annotations: readOnly,
	}, h.getTree)

	mcp.AddTool(server, &mcp.Tool{
		Name: "search_nodes",
		Description: "Find topics whose name contains the query (case-insensitive substring), across all trees, with the path to each one. " +
			"Matching is literal, so search a word stem to catch variants: \"polic\" finds both \"Policy\" and \"Policies\".",
		Annotations: readOnly,
	}, h.searchNodes)

	h.addWriteTools(server)

	return server
}

type handlers struct {
	repos Repositories
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

type listTreesInput struct{}

func (h *handlers) listTrees(ctx context.Context, req *mcp.CallToolRequest, in listTreesInput) (*mcp.CallToolResult, any, error) {
	trees, err := h.repos.Trees.GetAll()

	if err != nil {
		return nil, nil, err
	}

	if len(trees) == 0 {
		return text("There are no knowledge trees yet."), nil, nil
	}

	sort.Slice(trees, func(i, j int) bool { return trees[i].Name < trees[j].Name })

	var out strings.Builder

	for _, tree := range trees {
		nodes, err := h.repos.Nodes.FindByTree(tree.ID)

		if err != nil {
			return nil, nil, err
		}

		complete := 0

		for _, n := range nodes {
			if n.Status == domain.StatusComplete {
				complete++
			}
		}

		fmt.Fprintf(&out, "%s [%s]  %d/%d complete\n", tree.Name, lookup.ShortID(tree.ID), complete, len(nodes))
	}

	return text(out.String()), nil, nil
}

type getTreeInput struct {
	Tree  string `json:"tree,omitempty" jsonschema:"tree name or ID, as shown by list_trees. Optional when node is given."`
	Node  string `json:"node,omitempty" jsonschema:"node ID or ID prefix. When given, only this node's branch is shown."`
	Depth int    `json:"depth,omitempty" jsonschema:"levels to expand below the starting point. Default 2. Use -1 for everything."`
}

const defaultDepth = 2

// maxLines caps an outline so a depth -1 request on a huge tree can't flood
// the agent's context.
const maxLines = 1500

func (h *handlers) getTree(ctx context.Context, req *mcp.CallToolRequest, in getTreeInput) (*mcp.CallToolResult, any, error) {
	if in.Tree == "" && in.Node == "" {
		return nil, nil, fmt.Errorf("give a tree, a node, or both")
	}

	depth := in.Depth

	if depth == 0 {
		depth = defaultDepth
	}

	var start *domain.Node
	var tree *domain.KnowledgeTree
	var err error

	if in.Node != "" {
		if start, err = lookup.Node(h.repos.Nodes, in.Node); err != nil {
			return nil, nil, err
		}

		if tree, err = h.repos.Trees.FindByID(start.GraphID); err != nil {
			return nil, nil, err
		}
	} else if tree, err = lookup.Tree(h.repos.Trees, in.Tree); err != nil {
		return nil, nil, err
	}

	outline, err := h.outline(tree, start, depth)

	if err != nil {
		return nil, nil, err
	}

	return text(outline), nil, nil
}

// outline renders a whole tree (start nil) or one branch of it, depth levels
// deep.
func (h *handlers) outline(tree *domain.KnowledgeTree, start *domain.Node, depth int) (string, error) {
	snap, err := h.snapshot(tree)

	if err != nil {
		return "", err
	}

	var out strings.Builder
	parent := uuid.Nil

	if start == nil {
		done, total := snap.countAll()
		fmt.Fprintf(&out, "%s [%s]  %d/%d complete\n", tree.Name, lookup.ShortID(tree.ID), done, total)
	} else {
		fmt.Fprintf(&out, "%s %s [%s]", status(start), snap.path(start.ID), lookup.ShortID(start.ID))

		if done, total := snap.counts(start.ID); total > 0 {
			fmt.Fprintf(&out, "  %d/%d complete", done, total)
		}

		fmt.Fprintf(&out, "%s\n", snap.annotations(start.ID))
		parent = start.ID
	}

	r := &renderer{snap: snap, out: &out, maxDepth: depth}
	r.render(parent, "", 1)

	if r.truncated {
		fmt.Fprintf(&out, "… output truncated at %d lines; expand a branch with node=<id> instead\n", maxLines)
	}

	return out.String(), nil
}

type searchInput struct {
	Query string `json:"query" jsonschema:"text to look for in topic names, case-insensitive"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum number of results. Default 25."`
}

func (h *handlers) searchNodes(ctx context.Context, req *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, any, error) {
	query := strings.TrimSpace(in.Query)

	if query == "" {
		return nil, nil, fmt.Errorf("query cannot be empty")
	}

	limit := in.Limit

	if limit <= 0 {
		limit = 25
	}

	nodes, err := h.repos.Nodes.Search(query, limit)

	if err != nil {
		return nil, nil, err
	}

	if len(nodes) == 0 {
		return text(fmt.Sprintf("No topics match %q.", query)), nil, nil
	}

	snaps := map[uuid.UUID]*snapshot{}
	var out strings.Builder

	for _, node := range nodes {
		snap, ok := snaps[node.GraphID]

		if !ok {
			tree, err := h.repos.Trees.FindByID(node.GraphID)

			if err != nil {
				return nil, nil, err
			}

			if snap, err = h.snapshot(tree); err != nil {
				return nil, nil, err
			}

			snaps[node.GraphID] = snap
		}

		fmt.Fprintf(&out, "%s %s [%s]", status(node), node.Name, lookup.ShortID(node.ID))

		if kids := len(snap.children[node.ID]); kids > 0 {
			done, total := snap.counts(node.ID)
			fmt.Fprintf(&out, "  %d/%d", done, total)
		}

		fmt.Fprintf(&out, "  in %s\n", snap.path(snap.parentOf(node.ID)))
	}

	if len(nodes) == limit {
		fmt.Fprintf(&out, "(showing the first %d; narrow the query or raise limit for more)\n", limit)
	}

	return text(out.String()), nil, nil
}

func status(n *domain.Node) string {
	if n.Status == domain.StatusComplete {
		return "✓"
	}

	return "○"
}
