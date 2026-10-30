// Package graphml imports knowledge trees from yEd Live / yFiles GraphML
// exports, plain or gzip-compressed.
//
// The drawings follow these conventions, which Parse turns into a tree:
//   - edges point from parent to child, and each root becomes a tree
//   - a vertical chain of single-child nodes (A -> B -> C -> D) is a list of
//     ordered subtopics, so B, C and D all become children of A
//   - green fills mark completed topics
//   - unlabelled nodes are skipped and their children move up a level
//   - a node joined from several branches moves under their nearest common
//     ancestor, after its other children
package graphml

import (
	"bufio"
	"compress/gzip"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"jakegodsall/knowledge-tree/src/domain"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// DefaultCompleteColours are the green fills yEd Live uses for completed topics.
var DefaultCompleteColours = []string{"#FF7CB342", "#FF8BC34A"}

type Options struct {
	// CompleteColours are ARGB fills (e.g. #FF7CB342) that mark a node
	// complete. Matching ignores case. Defaults to DefaultCompleteColours.
	CompleteColours []string
}

type ImportedTree struct {
	Tree *domain.KnowledgeTree
	// Nodes are ordered so every parent comes before its children.
	Nodes []*domain.Node
}

type Result struct {
	Trees             []*ImportedTree
	SkippedUnlabelled int
	DuplicateEdges    int
	MergedNodes       int
	// ChainedNodes counts nodes moved up to the first node of their chain.
	ChainedNodes int
}

func Parse(r io.Reader, opts Options) (*Result, error) {
	reader, err := decompress(r)

	if err != nil {
		return nil, err
	}

	var doc element

	if err := xml.NewDecoder(reader).Decode(&doc); err != nil {
		return nil, fmt.Errorf("could not parse GraphML: %w", err)
	}

	if doc.XMLName.Local != "graphml" {
		return nil, fmt.Errorf("not a GraphML document: root element is <%s>", doc.XMLName.Local)
	}

	colours := opts.CompleteColours

	if len(colours) == 0 {
		colours = DefaultCompleteColours
	}

	p := &parser{
		resources: map[string]*element{},
		complete:  map[string]bool{},
	}

	for _, colour := range colours {
		p.complete[strings.ToUpper(colour)] = true
	}

	g, err := p.read(&doc)

	if err != nil {
		return nil, err
	}

	result := &Result{}
	result.DuplicateEdges = g.duplicateEdges
	result.SkippedUnlabelled = g.spliceUnlabelled()
	result.MergedNodes = g.resolveMerges()
	result.ChainedNodes = g.flattenChains()

	trees, err := g.build()

	if err != nil {
		return nil, err
	}

	result.Trees = trees
	return result, nil
}

func decompress(r io.Reader) (io.Reader, error) {
	buffered := bufio.NewReader(r)
	magic, _ := buffered.Peek(2)

	if len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(buffered)

		if err != nil {
			return nil, fmt.Errorf("could not decompress GraphML: %w", err)
		}

		return gz, nil
	}

	return buffered, nil
}

// element is a generic XML node. yFiles styling is deeply nested and varies
// between versions, so it's easier to walk a generic tree than map it to structs.
type element struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Children []element  `xml:",any"`
	Text     string     `xml:",chardata"`
}

func (e *element) attr(local string) string {
	for _, a := range e.Attrs {
		if a.Name.Local == local {
			return a.Value
		}
	}

	return ""
}

func (e *element) child(local string) *element {
	for i := range e.Children {
		if e.Children[i].XMLName.Local == local {
			return &e.Children[i]
		}
	}

	return nil
}

// find returns the first descendant with the given local name, depth first.
func (e *element) find(local string) *element {
	for i := range e.Children {
		c := &e.Children[i]

		if c.XMLName.Local == local {
			return c
		}

		if found := c.find(local); found != nil {
			return found
		}
	}

	return nil
}

func (e *element) findAll(local string, into *[]*element) {
	for i := range e.Children {
		c := &e.Children[i]

		if c.XMLName.Local == local {
			*into = append(*into, c)
		}

		c.findAll(local, into)
	}
}

type parser struct {
	labelKey, styleKey, geometryKey string
	resources                       map[string]*element
	complete                        map[string]bool
}

var (
	referencePattern = regexp.MustCompile(`^\{y:GraphMLReference\s+(\S+)\}$`)
	// only known HTML tags, so labels like "chan<- T" or "Vec<T>" survive
	tagPattern = regexp.MustCompile(`(?i)</?(p|span|div|strong|b|i|u|em|br|a|font|sub|sup|code)\b[^>]*>`)
)

func (p *parser) read(doc *element) (*graph, error) {
	for _, key := range doc.Children {
		if key.XMLName.Local != "key" {
			continue
		}

		switch key.attr("attr.name") {
		case "NodeLabels":
			p.labelKey = key.attr("id")
		case "NodeStyle":
			p.styleKey = key.attr("id")
		case "NodeGeometry":
			p.geometryKey = key.attr("id")
		}
	}

	if p.labelKey == "" {
		return nil, fmt.Errorf("no NodeLabels key found, is this a yEd Live export?")
	}

	var shared []*element
	doc.findAll("SharedData", &shared)

	for _, s := range shared {
		for i := range s.Children {
			if key := s.Children[i].attr("Key"); key != "" {
				p.resources[key] = &s.Children[i]
			}
		}
	}

	root := doc.child("graph")

	if root == nil {
		return nil, fmt.Errorf("GraphML document has no <graph>")
	}

	g := newGraph()

	for i := range root.Children {
		el := &root.Children[i]

		if el.XMLName.Local != "node" {
			continue
		}

		if el.child("graph") != nil {
			return nil, fmt.Errorf("node %s is a group node, which isn't supported", el.attr("id"))
		}

		g.addNode(p.node(el))
	}

	for i := range root.Children {
		el := &root.Children[i]

		if el.XMLName.Local != "edge" {
			continue
		}

		source, target := el.attr("source"), el.attr("target")

		if !g.has(source) || !g.has(target) {
			return nil, fmt.Errorf("edge %s refers to a missing node", el.attr("id"))
		}

		if source == target {
			continue
		}

		if !g.addEdge(source, target) {
			g.duplicateEdges++
		}
	}

	return g, nil
}

func (p *parser) node(el *element) *rawNode {
	n := &rawNode{id: el.attr("id")}

	for i := range el.Children {
		data := &el.Children[i]

		if data.XMLName.Local != "data" {
			continue
		}

		switch data.attr("key") {
		case p.labelKey:
			n.label = labelText(data.find("Label"))
		case p.styleKey:
			if len(data.Children) > 0 {
				n.complete = p.complete[strings.ToUpper(p.fill(&data.Children[0]))]
			}
		case p.geometryKey:
			if rect := data.find("RectD"); rect != nil {
				n.x, _ = strconv.ParseFloat(rect.attr("X"), 64)
				n.y, _ = strconv.ParseFloat(rect.attr("Y"), 64)
			}
		}
	}

	return n
}

// labelText reads a plain label (Text attribute) or a rich-text label (HTML
// inside a Label.Text element) and returns it as plain text.
func labelText(label *element) string {
	if label == nil {
		return ""
	}

	text := label.attr("Text")

	if text == "" {
		if rich := label.child("Label.Text"); rich != nil {
			text = rich.Text
		}
	}

	// plain labels can contain pasted HTML too, so clean both kinds
	text = html.UnescapeString(tagPattern.ReplaceAllString(text, " "))
	text = strings.Map(func(r rune) rune {
		if zeroWidth[r] {
			return -1
		}

		return r
	}, text)

	return strings.Join(strings.Fields(text), " ")
}

// zeroWidth characters sneak in from pasted text and aren't whitespace to
// strings.Fields.
var zeroWidth = map[rune]bool{
	0x200B: true, // zero width space
	0x200C: true, // zero width non-joiner
	0x200D: true, // zero width joiner
	0x2060: true, // word joiner
	0xFEFF: true, // zero width no-break space / BOM
}

func (p *parser) fill(style *element) string {
	if style.XMLName.Local == "GraphMLReference" {
		resource := p.resources[style.attr("ResourceKey")]

		if resource == nil {
			return ""
		}

		style = resource
	}

	if value := style.attr("fill"); value != "" {
		if m := referencePattern.FindStringSubmatch(value); m != nil {
			if resource := p.resources[m[1]]; resource != nil {
				return colourOf(resource)
			}

			return ""
		}

		return value
	}

	for i := range style.Children {
		c := &style.Children[i]

		if strings.HasSuffix(c.XMLName.Local, ".fill") && len(c.Children) > 0 {
			return colourOf(&c.Children[0])
		}
	}

	return ""
}

func colourOf(e *element) string {
	for _, name := range []string{"value", "cssString", "color"} {
		if v := e.attr(name); v != "" {
			return v
		}
	}

	return ""
}

type rawNode struct {
	id       string
	label    string
	complete bool
	x, y     float64
}

type graph struct {
	nodes    map[string]*rawNode
	order    []string // file order, for deterministic output
	parents  map[string][]string
	children map[string][]string

	duplicateEdges int
	// mergedLast marks nodes placed after their siblings by resolveMerges.
	mergedLast map[string]bool
	// chainDepth is a node's distance below the head of its chain.
	chainDepth map[string]int
}

func newGraph() *graph {
	return &graph{
		nodes:      map[string]*rawNode{},
		parents:    map[string][]string{},
		children:   map[string][]string{},
		mergedLast: map[string]bool{},
		chainDepth: map[string]int{},
	}
}

func (g *graph) addNode(n *rawNode) {
	g.nodes[n.id] = n
	g.order = append(g.order, n.id)
}

func (g *graph) has(id string) bool {
	_, ok := g.nodes[id]
	return ok
}

// addEdge reports false if the edge already exists.
func (g *graph) addEdge(source, target string) bool {
	for _, c := range g.children[source] {
		if c == target {
			return false
		}
	}

	g.children[source] = append(g.children[source], target)
	g.parents[target] = append(g.parents[target], source)
	return true
}

func (g *graph) removeEdge(source, target string) {
	g.children[source] = without(g.children[source], target)
	g.parents[target] = without(g.parents[target], source)
}

func without(ids []string, id string) []string {
	kept := ids[:0]

	for _, x := range ids {
		if x != id {
			kept = append(kept, x)
		}
	}

	return kept
}

// spliceUnlabelled removes unlabelled nodes, attaching their children to their
// parents. Unlabelled roots leave their children as roots.
func (g *graph) spliceUnlabelled() int {
	skipped := 0

	for _, id := range g.order {
		n, ok := g.nodes[id]

		if !ok || n.label != "" {
			continue
		}

		parents := append([]string{}, g.parents[id]...)
		children := append([]string{}, g.children[id]...)

		for _, c := range children {
			g.removeEdge(id, c)

			for _, p := range parents {
				g.addEdge(p, c)
			}
		}

		for _, p := range parents {
			g.removeEdge(p, id)
		}

		delete(g.nodes, id)
		delete(g.parents, id)
		delete(g.children, id)
		skipped++
	}

	return skipped
}

// ancestors returns id and everything above it, following every parent.
func (g *graph) ancestors(id string) map[string]bool {
	seen := map[string]bool{}
	stack := []string{id}

	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if seen[current] {
			continue
		}

		seen[current] = true
		stack = append(stack, g.parents[current]...)
	}

	return seen
}

// resolveMerges gives every node a single parent. A node with several parents
// moves under the lowest ancestor they share, or its first parent if they
// share none.
func (g *graph) resolveMerges() int {
	merged := 0

	for _, id := range g.order {
		parents := g.parents[id]

		if len(parents) <= 1 {
			continue
		}

		common := g.ancestors(parents[0])

		for _, p := range parents[1:] {
			other := g.ancestors(p)

			for a := range common {
				if !other[a] {
					delete(common, a)
				}
			}
		}

		delete(common, id)
		target := parents[0]

		// the lowest common ancestor is one that isn't above another one
		for _, candidate := range g.order {
			if !common[candidate] {
				continue
			}

			lowest := true

			for other := range common {
				if other != candidate && g.ancestors(other)[candidate] {
					lowest = false
					break
				}
			}

			if lowest {
				target = candidate
				break
			}
		}

		for _, p := range append([]string{}, parents...) {
			g.removeEdge(p, id)
		}

		g.addEdge(target, id)
		g.mergedLast[id] = true
		merged++
	}

	return merged
}

func (g *graph) parentOf(id string) (string, bool) {
	if len(g.parents[id]) == 0 {
		return "", false
	}

	return g.parents[id][0], true
}

// flattenChains moves every node in a run of single-child nodes up to the
// run's first node. Runs are worked out from the original shape before
// anything moves.
func (g *graph) flattenChains() int {
	newParent := map[string]string{}

	for _, id := range g.order {
		if !g.has(id) || g.mergedLast[id] {
			continue
		}

		parent, ok := g.parentOf(id)

		if !ok || len(g.children[parent]) != 1 {
			continue
		}

		head, depth := parent, 1

		for {
			grandparent, ok := g.parentOf(head)

			if !ok || len(g.children[grandparent]) != 1 || g.mergedLast[head] {
				break
			}

			head = grandparent
			depth++
		}

		g.chainDepth[id] = depth

		if head != parent {
			newParent[id] = head
		}
	}

	for id, head := range newParent {
		parent, _ := g.parentOf(id)
		g.removeEdge(parent, id)
		g.addEdge(head, id)
	}

	return len(newParent)
}

func (g *graph) build() ([]*ImportedTree, error) {
	trees := []*ImportedTree{}
	names := map[string]bool{}
	visited := 0

	for _, id := range g.order {
		if !g.has(id) {
			continue
		}

		if _, hasParent := g.parentOf(id); hasParent {
			continue
		}

		root := g.nodes[id]

		if names[root.label] {
			return nil, fmt.Errorf("more than one tree is called %q", root.label)
		}

		names[root.label] = true
		imported := &ImportedTree{Tree: domain.NewKnowledgeTree(root.label)}

		if err := g.addChildren(imported, id, nil, &visited); err != nil {
			return nil, err
		}

		visited++
		trees = append(trees, imported)
	}

	if visited != len(g.nodes) {
		return nil, fmt.Errorf("graph contains a cycle: %d nodes can't be reached from a root", len(g.nodes)-visited)
	}

	return trees, nil
}

func (g *graph) addChildren(tree *ImportedTree, id string, parentID *uuid.UUID, visited *int) error {
	children := g.sortedChildren(id)

	if len(children) > math.MaxUint16 {
		return fmt.Errorf("node %q has too many children (%d)", g.nodes[id].label, len(children))
	}

	for position, childID := range children {
		raw := g.nodes[childID]
		node := domain.NewNode(tree.Tree.ID, parentID, raw.label, uint16(position))

		if raw.complete {
			node.Complete()
		}

		tree.Nodes = append(tree.Nodes, node)
		*visited++

		if err := g.addChildren(tree, childID, &node.ID, visited); err != nil {
			return err
		}
	}

	return nil
}

// sortedChildren orders chain members by their place in the chain and other
// children left to right, then top to bottom. Merged nodes go last.
func (g *graph) sortedChildren(id string) []string {
	children := append([]string{}, g.children[id]...)

	sort.SliceStable(children, func(i, j int) bool {
		a, b := children[i], children[j]

		if g.mergedLast[a] != g.mergedLast[b] {
			return !g.mergedLast[a]
		}

		if g.chainDepth[a] != g.chainDepth[b] {
			return g.chainDepth[a] < g.chainDepth[b]
		}

		if g.nodes[a].x != g.nodes[b].x {
			return g.nodes[a].x < g.nodes[b].x
		}

		return g.nodes[a].y < g.nodes[b].y
	})

	return children
}
