package sqlite

import (
	"database/sql"
	"fmt"
	"jakegodsall/knowledge-tree/src/domain"
	"strings"
	"time"

	"github.com/google/uuid"
)

type NodeRepository struct {
	db *sql.DB
}

func NewNodeRepository(db *sql.DB) *NodeRepository {
	return &NodeRepository{
		db: db,
	}
}

func (r *NodeRepository) Create(node *domain.Node) error {
	res, err := r.db.Exec(`
			INSERT INTO nodes (id, graph_id, parent_id, name, status, position, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`,
		&node.ID,
		&node.GraphID,
		&node.ParentID,
		&node.Name,
		&node.Status,
		&node.Position,
		&node.CreatedAt,
		&node.UpdatedAt,
	)

	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()

	if err != nil {
		return err
	}

	if rows == 0 {
		return fmt.Errorf("could not create the node %s", node.ID.String())
	}

	return nil
}

func (r *NodeRepository) FindByID(id uuid.UUID) (*domain.Node, error) {
	row := r.db.QueryRow(`
			SELECT id, graph_id, parent_id, name, status, position, created_at, updated_at
			FROM nodes
			WHERE id = ?
		`,
		id,
	)

	node := &domain.Node{}

	if err := row.Scan(
		&node.ID,
		&node.GraphID,
		&node.ParentID,
		&node.Name,
		&node.Status,
		&node.Position,
		&node.CreatedAt,
		&node.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no node found for id %s", id)
		}
		return nil, err
	}

	return node, nil
}

func (r *NodeRepository) FindByIDPrefix(prefix string) ([]*domain.Node, error) {
	prefix = strings.ToLower(prefix)

	return r.query(`
			SELECT id, graph_id, parent_id, name, status, position, created_at, updated_at
			FROM nodes
			WHERE substr(id, 1, length(?)) = ?
		`,
		prefix,
		prefix,
	)
}

func (r *NodeRepository) FindByTree(graphID uuid.UUID) ([]*domain.Node, error) {
	return r.query(`
			SELECT id, graph_id, parent_id, name, status, position, created_at, updated_at
			FROM nodes
			WHERE graph_id = ?
			ORDER BY position
		`,
		graphID,
	)
}

func (r *NodeRepository) FindRoots(graphID uuid.UUID) ([]*domain.Node, error) {
	return r.query(`
			SELECT id, graph_id, parent_id, name, status, position, created_at, updated_at
			FROM nodes
			WHERE graph_id = ? AND parent_id IS NULL
			ORDER BY position
		`,
		graphID,
	)
}

func (r *NodeRepository) FindChildren(parentID uuid.UUID) ([]*domain.Node, error) {
	return r.query(`
			SELECT id, graph_id, parent_id, name, status, position, created_at, updated_at
			FROM nodes
			WHERE parent_id = ?
			ORDER BY position
		`,
		parentID,
	)
}

func (r *NodeRepository) Search(query string, limit int) ([]*domain.Node, error) {
	// escape LIKE wildcards so "%" and "_" in the query match literally
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query)

	// shortest names first, so exact and near-exact matches come out on top
	return r.query(`
			SELECT id, graph_id, parent_id, name, status, position, created_at, updated_at
			FROM nodes
			WHERE name LIKE ? ESCAPE '\'
			ORDER BY length(name), name
			LIMIT ?
		`,
		"%"+escaped+"%",
		limit,
	)
}

func (r *NodeRepository) query(query string, args ...any) ([]*domain.Node, error) {
	rows, err := r.db.Query(query, args...)

	if err != nil {
		return nil, err
	}

	defer rows.Close()

	nodes := []*domain.Node{}

	for rows.Next() {
		node := &domain.Node{}

		if err := rows.Scan(
			&node.ID,
			&node.GraphID,
			&node.ParentID,
			&node.Name,
			&node.Status,
			&node.Position,
			&node.CreatedAt,
			&node.UpdatedAt,
		); err != nil {
			return nil, err
		}

		nodes = append(nodes, node)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return nodes, nil
}

func (r *NodeRepository) Update(node *domain.Node) error {
	res, err := r.db.Exec(`
			UPDATE nodes
			SET graph_id = ?, parent_id = ?, name = ?, status = ?, position = ?, updated_at = ?
			WHERE id = ?
		`,
		&node.GraphID,
		&node.ParentID,
		&node.Name,
		&node.Status,
		&node.Position,
		&node.UpdatedAt,
		&node.ID,
	)

	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()

	if err != nil {
		return err
	}

	if rows == 0 {
		return fmt.Errorf("node %s could not be updated", node.ID.String())
	}

	return nil
}

func (r *NodeRepository) InsertAt(node *domain.Node, index int) error {
	tx, err := r.db.Begin()

	if err != nil {
		return err
	}

	defer tx.Rollback()

	positions, err := siblingPositions(tx, node.GraphID, node.ParentID, uuid.Nil)

	if err != nil {
		return err
	}

	node.Position = positionForIndex(positions, index)

	if err := shiftSiblings(tx, node.GraphID, node.ParentID, node.Position, uuid.Nil); err != nil {
		return err
	}

	_, err = tx.Exec(`
			INSERT INTO nodes (id, graph_id, parent_id, name, status, position, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`,
		node.ID,
		node.GraphID,
		parentArg(node.ParentID),
		node.Name,
		node.Status,
		node.Position,
		node.CreatedAt,
		node.UpdatedAt,
	)

	if err != nil {
		return err
	}

	return tx.Commit()
}

func (r *NodeRepository) Move(id uuid.UUID, parentID *uuid.UUID, index int) error {
	tx, err := r.db.Begin()

	if err != nil {
		return err
	}

	defer tx.Rollback()

	var graphID uuid.UUID
	var oldParent *uuid.UUID
	var oldPosition uint16

	err = tx.QueryRow("SELECT graph_id, parent_id, position FROM nodes WHERE id = ?", id).
		Scan(&graphID, &oldParent, &oldPosition)

	if err == sql.ErrNoRows {
		return fmt.Errorf("no node found for id %s", id)
	}

	if err != nil {
		return err
	}

	if parentID != nil {
		var parentGraph uuid.UUID

		err := tx.QueryRow("SELECT graph_id FROM nodes WHERE id = ?", *parentID).Scan(&parentGraph)

		if err == sql.ErrNoRows {
			return fmt.Errorf("no node found for id %s", *parentID)
		}

		if err != nil {
			return err
		}

		if parentGraph != graphID {
			return fmt.Errorf("node %s can't move to another tree", id)
		}

		var insideSubtree bool

		err = tx.QueryRow(`
				WITH RECURSIVE subtree(id) AS (
					SELECT ?
					UNION ALL
					SELECT n.id FROM nodes n JOIN subtree s ON n.parent_id = s.id
				)
				SELECT EXISTS (SELECT 1 FROM subtree WHERE id = ?)
			`,
			id,
			*parentID,
		).Scan(&insideSubtree)

		if err != nil {
			return err
		}

		if insideSubtree {
			return fmt.Errorf("node %s can't move under itself or one of its descendants", id)
		}
	}

	// close the gap at the old position, then open one at the new position
	if err := closeGap(tx, graphID, oldParent, oldPosition, id); err != nil {
		return err
	}

	positions, err := siblingPositions(tx, graphID, parentID, id)

	if err != nil {
		return err
	}

	position := positionForIndex(positions, index)

	if err := shiftSiblings(tx, graphID, parentID, position, id); err != nil {
		return err
	}

	_, err = tx.Exec(
		"UPDATE nodes SET parent_id = ?, position = ?, updated_at = ? WHERE id = ?",
		parentArg(parentID),
		position,
		time.Now(),
		id,
	)

	if err != nil {
		return err
	}

	return tx.Commit()
}

func (r *NodeRepository) DeleteByID(id uuid.UUID) error {
	tx, err := r.db.Begin()

	if err != nil {
		return err
	}

	defer tx.Rollback()

	var graphID uuid.UUID
	var parentID *uuid.UUID
	var position uint16

	err = tx.QueryRow("SELECT graph_id, parent_id, position FROM nodes WHERE id = ?", id).
		Scan(&graphID, &parentID, &position)

	if err == sql.ErrNoRows {
		return fmt.Errorf("node %s could not be deleted", id.String())
	}

	if err != nil {
		return err
	}

	// nodes.parent_id has no foreign key, so walk the subtree explicitly.
	// Prerequisites and tags are removed by their ON DELETE CASCADE.
	_, err = tx.Exec(`
			WITH RECURSIVE subtree(id) AS (
				SELECT ?
				UNION ALL
				SELECT n.id FROM nodes n JOIN subtree s ON n.parent_id = s.id
			)
			DELETE FROM nodes WHERE id IN subtree
		`,
		id,
	)

	if err != nil {
		return err
	}

	if err := closeGap(tx, graphID, parentID, position, id); err != nil {
		return err
	}

	return tx.Commit()
}

// parentArg turns a missing parent into SQL NULL. `parent_id IS ?` then
// matches top-level nodes as well as children.
func parentArg(parentID *uuid.UUID) any {
	if parentID == nil {
		return nil
	}

	return parentID.String()
}

// siblingPositions returns the positions under a parent in order, leaving
// out exclude (uuid.Nil excludes nothing).
func siblingPositions(tx *sql.Tx, graphID uuid.UUID, parentID *uuid.UUID, exclude uuid.UUID) ([]uint16, error) {
	rows, err := tx.Query(`
			SELECT position FROM nodes
			WHERE graph_id = ? AND parent_id IS ? AND id <> ?
			ORDER BY position
		`,
		graphID,
		parentArg(parentID),
		exclude,
	)

	if err != nil {
		return nil, err
	}

	defer rows.Close()

	positions := []uint16{}

	for rows.Next() {
		var p uint16

		if err := rows.Scan(&p); err != nil {
			return nil, err
		}

		positions = append(positions, p)
	}

	return positions, rows.Err()
}

// positionForIndex converts a 0-based index among siblings into a position
// value. Indexes past the end append.
func positionForIndex(positions []uint16, index int) uint16 {
	if index < 0 {
		index = 0
	}

	if index < len(positions) {
		return positions[index]
	}

	if len(positions) == 0 {
		return 0
	}

	return positions[len(positions)-1] + 1
}

// shiftSiblings moves siblings at or after position down one to make room.
func shiftSiblings(tx *sql.Tx, graphID uuid.UUID, parentID *uuid.UUID, position uint16, exclude uuid.UUID) error {
	_, err := tx.Exec(`
			UPDATE nodes SET position = position + 1
			WHERE graph_id = ? AND parent_id IS ? AND position >= ? AND id <> ?
		`,
		graphID,
		parentArg(parentID),
		position,
		exclude,
	)

	return err
}

// closeGap moves siblings after position up one once a node has left.
func closeGap(tx *sql.Tx, graphID uuid.UUID, parentID *uuid.UUID, position uint16, exclude uuid.UUID) error {
	_, err := tx.Exec(`
			UPDATE nodes SET position = position - 1
			WHERE graph_id = ? AND parent_id IS ? AND position > ? AND id <> ?
		`,
		graphID,
		parentArg(parentID),
		position,
		exclude,
	)

	return err
}
