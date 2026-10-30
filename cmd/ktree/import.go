package main

import (
	"flag"
	"fmt"
	"jakegodsall/knowledge-tree/src/domain"
	"jakegodsall/knowledge-tree/src/graphml"
	"os"
	"strings"
	"time"
)

func (a *app) runImport(args []string) error {
	flags := flag.NewFlagSet("import", flag.ContinueOnError)
	dryRun := flags.Bool("dry-run", false, "show what would be imported without saving")
	colours := flags.String("complete-colours", strings.Join(graphml.DefaultCompleteColours, ","), "comma-separated ARGB fills that mark a node complete")

	positional, err := parseArgs(flags, args)

	if err != nil {
		return err
	}

	if len(positional) != 1 {
		return fmt.Errorf("usage: ktree import <file.graphml> [--dry-run] [--complete-colours #FF7CB342,...]")
	}

	file, err := os.Open(positional[0])

	if err != nil {
		return err
	}

	defer file.Close()

	result, err := graphml.Parse(file, graphml.Options{
		CompleteColours: strings.Split(*colours, ","),
	})

	if err != nil {
		return err
	}

	existing, err := a.trees.GetAll()

	if err != nil {
		return err
	}

	names := map[string]bool{}

	for _, tree := range existing {
		names[tree.Name] = true
	}

	// import is one-off: refuse before writing anything if a tree exists
	clashes := []string{}

	for _, imported := range result.Trees {
		if names[imported.Tree.Name] {
			clashes = append(clashes, imported.Tree.Name)
		}
	}

	if len(clashes) > 0 {
		return fmt.Errorf("these trees already exist, delete them first to re-import: %s", strings.Join(clashes, ", "))
	}

	totalNodes, totalComplete := 0, 0

	for _, imported := range result.Trees {
		complete := 0

		for _, node := range imported.Nodes {
			if node.Status == domain.StatusComplete {
				complete++
			}
		}

		totalNodes += len(imported.Nodes)
		totalComplete += complete
		fmt.Printf("  %-24s %5d nodes  %4d complete\n", imported.Tree.Name, len(imported.Nodes), complete)
	}

	fmt.Printf("%d trees, %d nodes, %d complete\n", len(result.Trees), totalNodes, totalComplete)
	fmt.Printf("skipped %d unlabelled nodes, collapsed %d duplicate edges, moved %d merged nodes, flattened %d chain nodes\n",
		result.SkippedUnlabelled, result.DuplicateEdges, result.MergedNodes, result.ChainedNodes)

	if *dryRun {
		fmt.Println("dry run: nothing saved")
		return nil
	}

	start := time.Now()

	for _, imported := range result.Trees {
		if err := a.trees.Create(imported.Tree); err != nil {
			return fmt.Errorf("could not save tree %s: %w", imported.Tree.Name, err)
		}

		for _, node := range imported.Nodes {
			if err := a.nodes.Create(node); err != nil {
				return fmt.Errorf("could not save node %s in %s: %w", node.Name, imported.Tree.Name, err)
			}
		}
	}

	fmt.Printf("imported in %s\n", time.Since(start).Round(time.Millisecond))
	return nil
}
