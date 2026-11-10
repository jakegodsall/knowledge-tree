package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"jakegodsall/knowledge-tree/src/domain"
	"jakegodsall/knowledge-tree/src/lookup"
	"jakegodsall/knowledge-tree/src/repository"
	"slices"
	"strings"

	"github.com/google/uuid"
)

const usage = `usage: ktree <command> [arguments]

trees:
  list                                   list all trees
  create <name>                          create a tree
  show <tree> [--node <node>]            show a tree's nodes, optionally from a subtree
  delete-tree <tree>                     delete a tree and all its nodes
  demote-tree <tree> (--parent <node> | --into <tree>) [--name <name>]
                                         turn a tree into a node of another
                                         tree; --into creates the tree if needed

nodes:
  add-node <tree> <name> [--parent <node>]  add a root or child node
  move-node <node> --index <n> [--parent <node>]
                                            move a node to position n among its
                                            siblings; optionally reparent it
  update-node <node> --name <name>          rename a node
  delete-node <node>                        delete a node and its children
  complete <node>                           mark a node complete

study order:
  require <node> <required-node>         learn <node> after <required-node>
  tag <node> <tag>                       tag a node, e.g. sap-c02

import:
  import <file.graphml> [--dry-run]      import trees from a yEd Live export

<tree> is a tree name or ID. <node> is a node ID or any unique prefix of
one, as printed by show.`

type app struct {
	trees         repository.KnowledgeTreeRepository
	nodes         repository.NodeRepository
	prerequisites repository.PrerequisiteRepository
	tags          repository.NodeTagRepository
}

func (a *app) runList(args []string) error {
	trees, err := a.trees.GetAll()

	if err != nil {
		return err
	}

	slices.SortFunc(trees, func (a, b *domain.KnowledgeTree) int {
		return strings.Compare(a.Name, b.Name)
	})

	for _, tree := range trees {
		fmt.Printf("%s  %s\n", shortID(tree.ID), tree.Name)
	}

	return nil
}

func (a *app) runCreate(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: ktree create <name>")
	}

	tree := domain.NewKnowledgeTree(args[0])

	if err := a.trees.Create(tree); err != nil {
		return err
	}

	fmt.Printf("created tree %s [%s]\n", tree.Name, shortID(tree.ID))
	return nil
}

func (a *app) runShow(args []string) error {
	flags := flag.NewFlagSet("show", flag.ContinueOnError)
	nodeArg := flags.String("node", "", "node ID or prefix to start from")
	expandArg := flags.Bool("expand", false, "expand completed nodes")

	positional, err := parseArgs(flags, args)
	if err != nil {
		return err
	}

	if len(positional) > 1 || (len(positional) == 0 && *nodeArg == "") {
		return fmt.Errorf("usage: ktree show <tree> [--node <node>]")
	}

	var tree *domain.KnowledgeTree
	var start *domain.Node

	if *nodeArg != "" {
		if start, err = a.resolveNode(*nodeArg); err != nil {
			return err
		}
		if tree, err = a.trees.FindByID(start.GraphID); err != nil {
			return err
		}
	} else {
		if tree, err = a.resolveTree(positional[0]); err != nil {
			return err
		}
	}

	nodes, err := a.nodes.FindByTree(tree.ID)
	if err != nil {
		return err
	}

	// group by parent; roots sit under uuid.Nil. Nodes arrive ordered by
	// position, so each group keeps its sibling order.
	children := map[uuid.UUID][]*domain.Node{}
	for _, n := range nodes {
		parent := uuid.Nil
		if n.ParentID != nil {
			parent = *n.ParentID
		}
		children[parent] = append(children[parent], n)
	}

	if start == nil {
		completed := 0
		for _, n := range nodes {
			if n.Status == domain.StatusComplete {
				completed++
			}
		}
		fmt.Printf("%s [%s]  %d/%d complete\n", tree.Name, shortID(tree.ID), completed, len(nodes))
		return a.printNodes(children, uuid.Nil, "", *expandArg)
	}

	done, total := subtreeCounts(children, start)
	status := "○"
	if start.Status == domain.StatusComplete {
		status = "✓"
	}
	fmt.Printf("%s %s [%s]  %d/%d complete\n", status, start.Name, shortID(start.ID), done, total)
	return a.printNodes(children, start.ID, "", *expandArg)
}

// subtreeCounts returns how many of a node's descendants are complete, and
// how many there are. The node itself is not counted.
func subtreeCounts(children map[uuid.UUID][]*domain.Node, n *domain.Node) (done, total int) {
	for _, child := range children[n.ID] {
		total++
		if child.Status == domain.StatusComplete {
			done++
		}
		d, t := subtreeCounts(children, child)
		done += d
		total += t
	}
	return
}

func (a *app) printNodes(children map[uuid.UUID][]*domain.Node, parent uuid.UUID, indent string, expand bool) error {
	siblings := children[parent]

	for i, node := range siblings {
		branch, childIndent := "├── ", "│   "

		if i == len(siblings)-1 {
			branch, childIndent = "└── ", "    "
		}

		status := "○"

		if node.Status == domain.StatusComplete {
			status = "✓"
		}

		tags, err := a.tags.FindByNode(node.ID)

		if err != nil {
			return err
		}

		done, total := subtreeCounts(children, node)

		var labels strings.Builder

		for _, tag := range tags {
			labels.WriteString(" #")
			labels.WriteString(tag.Tag)
		}

		if total > 0 {
			fmt.Printf("%s%s%s %s [%s]%s %d/%d complete\n", indent, branch, status, node.Name, shortID(node.ID), labels.String(), done, total)
		} else {
			fmt.Printf("%s%s%s %s [%s]%s\n", indent, branch, status, node.Name, shortID(node.ID), labels.String())
		}
		// collapse a branch only once it and everything beneath it is complete
		if expand || node.Status != domain.StatusComplete || done != total {
			if err := a.printNodes(children, node.ID, indent+childIndent, expand); err != nil {
				return err
			}
		}
	}

	return nil
}

func (a *app) runDeleteTree(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: ktree delete-tree <tree>")
	}

	tree, err := a.resolveTree(args[0])

	if err != nil {
		return err
	}

	if err := a.trees.DeleteByID(tree.ID); err != nil {
		return err
	}

	fmt.Printf("deleted tree %s\n", tree.Name)
	return nil
}

func (a *app) runDemoteTree(args []string) error {
	flags := flag.NewFlagSet("demote-tree", flag.ContinueOnError)
	parentArg := flags.String("parent", "", "parent node ID or prefix")
	intoArg := flags.String("into", "", "tree to put it at the top level of")
	nameArg := flags.String("name", "", "name for the new node; defaults to the tree's name")

	positional, err := parseArgs(flags, args)

	if err != nil {
		return err
	}

	if len(positional) != 1 || (*parentArg == "") == (*intoArg == "") {
		return fmt.Errorf("usage: ktree demote-tree <tree> (--parent <node> | --into <tree>) [--name <name>]")
	}

	source, err := a.resolveTree(positional[0])

	if err != nil {
		return err
	}

	name := strings.TrimSpace(*nameArg)

	if name == "" {
		name = source.Name
	}

	var target *domain.KnowledgeTree
	var newTree *domain.KnowledgeTree
	var parentID *uuid.UUID

	if *parentArg != "" {
		parent, err := a.resolveNode(*parentArg)

		if err != nil {
			return err
		}

		parentID = &parent.ID

		if target, err = a.trees.FindByID(parent.GraphID); err != nil {
			return err
		}
	} else {
		trees, err := a.trees.GetAll()

		if err != nil {
			return err
		}

		for _, t := range trees {
			if t.Name == *intoArg || t.ID.String() == *intoArg {
				target = t
			}
		}

		if target == nil {
			newTree = domain.NewKnowledgeTree(strings.TrimSpace(*intoArg))
			target = newTree
		}
	}

	node := domain.NewNode(target.ID, parentID, name, 0)

	if err := a.nodes.DemoteTree(source.ID, node, math.MaxInt32, newTree); err != nil {
		return err
	}

	if newTree != nil {
		fmt.Printf("created tree %s [%s]\n", newTree.Name, shortID(newTree.ID))
	}

	fmt.Printf("demoted tree %s to %s [%s] in %s\n", source.Name, node.Name, shortID(node.ID), target.Name)
	return nil
}

func (a *app) runAddNode(args []string) error {
	flags := flag.NewFlagSet("add-node", flag.ContinueOnError)
	parentArg := flags.String("parent", "", "parent node ID or prefix")

	positional, err := parseArgs(flags, args)

	if err != nil {
		return err
	}

	if len(positional) != 2 {
		return fmt.Errorf("usage: ktree add-node <tree> <name> [--parent <node>]")
	}

	tree, err := a.resolveTree(positional[0])

	if err != nil {
		return err
	}

	var parentID *uuid.UUID
	var siblings []*domain.Node

	if *parentArg != "" {
		parent, err := a.resolveNode(*parentArg)

		if err != nil {
			return err
		}

		if parent.GraphID != tree.ID {
			return fmt.Errorf("parent %s [%s] is not in tree %s", parent.Name, shortID(parent.ID), tree.Name)
		}

		parentID = &parent.ID
		siblings, err = a.nodes.FindChildren(parent.ID)

		if err != nil {
			return err
		}
	} else {
		siblings, err = a.nodes.FindRoots(tree.ID)

		if err != nil {
			return err
		}
	}

	node := domain.NewNode(tree.ID, parentID, positional[1], uint16(len(siblings)))

	if err := a.nodes.Create(node); err != nil {
		return err
	}

	fmt.Printf("added %s [%s]\n", node.Name, shortID(node.ID))
	return nil
}

func (a *app) runUpdateNode(args []string) error {
	flags := flag.NewFlagSet("update-node", flag.ContinueOnError)
	name := flags.String("name", "", "new name")

	positional, err := parseArgs(flags, args)

	if err != nil {
		return err
	}

	if len(positional) != 1 || *name == "" {
		return fmt.Errorf("usage: ktree update-node <node> --name <name>")
	}

	node, err := a.resolveNode(positional[0])

	if err != nil {
		return err
	}

	old := node.Name
	node.Rename(*name)

	if err := a.nodes.Update(node); err != nil {
		return err
	}

	fmt.Printf("renamed %s to %s [%s]\n", old, node.Name, shortID(node.ID))
	return nil
}

func (a *app) runMoveNode(args []string) error {
	flags := flag.NewFlagSet("move-node", flag.ContinueOnError)
	indexArg := flags.Int("index", -1, "0-based position among siblings")
	parentArg := flags.String("parent", "", "new parent node ID or prefix (omit to keep current)")

	positional, err := parseArgs(flags, args)
	if err != nil {
		return err
	}

	if len(positional) != 1 || *indexArg < 0 {
		return fmt.Errorf("usage: ktree move-node <node> --index <n> [--parent <node>]")
	}

	node, err := a.resolveNode(positional[0])
	if err != nil {
		return err
	}

	var parentID *uuid.UUID

	if *parentArg != "" {
		parent, err := a.resolveNode(*parentArg)
		if err != nil {
			return err
		}
		parentID = &parent.ID
	} else {
		parentID = node.ParentID
	}

	if err := a.nodes.Move(node.ID, parentID, *indexArg); err != nil {
		return err
	}

	fmt.Printf("moved %s [%s] to position %d\n", node.Name, shortID(node.ID), *indexArg)
	return nil
}

func (a *app) runDeleteNode(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: ktree delete-node <node>")
	}

	node, err := a.resolveNode(args[0])

	if err != nil {
		return err
	}

	if err := a.nodes.DeleteByID(node.ID); err != nil {
		return err
	}

	fmt.Printf("deleted %s [%s] and its children\n", node.Name, shortID(node.ID))
	return nil
}

func (a *app) runComplete(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: ktree complete <node>")
	}

	node, err := a.resolveNode(args[0])

	if err != nil {
		return err
	}

	node.Complete()

	if err := a.nodes.Update(node); err != nil {
		return err
	}

	fmt.Printf("completed %s [%s]\n", node.Name, shortID(node.ID))
	return nil
}

func (a *app) runRequire(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: ktree require <node> <required-node>")
	}

	node, err := a.resolveNode(args[0])

	if err != nil {
		return err
	}

	required, err := a.resolveNode(args[1])

	if err != nil {
		return err
	}

	prerequisite, err := domain.NewPrerequisite(node.ID, required.ID)

	if err != nil {
		return err
	}

	err = a.prerequisites.Create(prerequisite)

	if errors.Is(err, domain.ErrPrerequisiteCycle) {
		return fmt.Errorf("%s already depends on %s, so it can't also be required by it", required.Name, node.Name)
	}

	if err != nil {
		return err
	}

	fmt.Printf("%s now requires %s\n", node.Name, required.Name)
	return nil
}

func (a *app) runTag(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: ktree tag <node> <tag>")
	}

	node, err := a.resolveNode(args[0])

	if err != nil {
		return err
	}

	tag, err := domain.NewNodeTag(node.ID, args[1])

	if err != nil {
		return err
	}

	if err := a.tags.Create(tag); err != nil {
		return err
	}

	fmt.Printf("tagged %s #%s\n", node.Name, tag.Tag)
	return nil
}

func (a *app) resolveTree(arg string) (*domain.KnowledgeTree, error) {
	return lookup.Tree(a.trees, arg)
}

func (a *app) resolveNode(arg string) (*domain.Node, error) {
	return lookup.Node(a.nodes, arg)
}

func parseArgs(flags *flag.FlagSet, args []string) ([]string, error) {
	positional := []string{}

	for {
		if err := flags.Parse(args); err != nil {
			return nil, err
		}

		args = flags.Args()

		if len(args) == 0 {
			return positional, nil
		}

		positional = append(positional, args[0])
		args = args[1:]
	}
}

func shortID(id uuid.UUID) string {
	return lookup.ShortID(id)
}
