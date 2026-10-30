package sqlite

import (
	"database/sql"
	"fmt"
	"jakegodsall/knowledge-tree/src/domain"

	"github.com/google/uuid"
)

type PrerequisiteRepository struct {
	db *sql.DB
}

func NewPrerequisiteRepository(db *sql.DB) *PrerequisiteRepository {
	return &PrerequisiteRepository{
		db: db,
	}
}

func (r *PrerequisiteRepository) Create(prerequisite *domain.Prerequisite) error {
	tx, err := r.db.Begin()

	if err != nil {
		return err
	}

	defer tx.Rollback()

	// Walk everything RequiresNodeID depends on. If NodeID is in there, the new
	// prerequisite would close a loop.
	var createsCycle bool

	err = tx.QueryRow(`
			WITH RECURSIVE required(id) AS (
				SELECT requires_node_id FROM node_prerequisites WHERE node_id = ?
				UNION
				SELECT p.requires_node_id
				FROM node_prerequisites p
				JOIN required r ON p.node_id = r.id
			)
			SELECT EXISTS (SELECT 1 FROM required WHERE id = ?)
		`,
		prerequisite.RequiresNodeID,
		prerequisite.NodeID,
	).Scan(&createsCycle)

	if err != nil {
		return err
	}

	if createsCycle {
		return fmt.Errorf("node %s requires node %s: %w", prerequisite.NodeID.String(), prerequisite.RequiresNodeID.String(), domain.ErrPrerequisiteCycle)
	}

	res, err := tx.Exec(`
			INSERT INTO node_prerequisites (node_id, requires_node_id, created_at)
			VALUES (?, ?, ?)
		`,
		prerequisite.NodeID,
		prerequisite.RequiresNodeID,
		prerequisite.CreatedAt,
	)

	if isDuplicate(err) {
		return fmt.Errorf("node %s already requires node %s", prerequisite.NodeID.String(), prerequisite.RequiresNodeID.String())
	}

	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()

	if err != nil {
		return err
	}

	if rows == 0 {
		return fmt.Errorf("could not create prerequisite %s -> %s", prerequisite.NodeID.String(), prerequisite.RequiresNodeID.String())
	}

	return tx.Commit()
}

func (r *PrerequisiteRepository) GetAll() ([]*domain.Prerequisite, error) {
	return r.query(`
			SELECT node_id, requires_node_id, created_at
			FROM node_prerequisites
		`)
}

func (r *PrerequisiteRepository) FindByNode(nodeID uuid.UUID) ([]*domain.Prerequisite, error) {
	return r.query(`
			SELECT node_id, requires_node_id, created_at
			FROM node_prerequisites
			WHERE node_id = ?
		`,
		nodeID,
	)
}

func (r *PrerequisiteRepository) FindDependents(nodeID uuid.UUID) ([]*domain.Prerequisite, error) {
	return r.query(`
			SELECT node_id, requires_node_id, created_at
			FROM node_prerequisites
			WHERE requires_node_id = ?
		`,
		nodeID,
	)
}

func (r *PrerequisiteRepository) Delete(nodeID uuid.UUID, requiresNodeID uuid.UUID) error {
	res, err := r.db.Exec(
		"DELETE FROM node_prerequisites WHERE node_id = ? AND requires_node_id = ?",
		nodeID,
		requiresNodeID,
	)

	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()

	if err != nil {
		return err
	}

	if rows == 0 {
		return fmt.Errorf("prerequisite %s -> %s could not be deleted", nodeID.String(), requiresNodeID.String())
	}

	return nil
}

func (r *PrerequisiteRepository) query(query string, args ...any) ([]*domain.Prerequisite, error) {
	rows, err := r.db.Query(query, args...)

	if err != nil {
		return nil, err
	}

	defer rows.Close()

	prerequisites := []*domain.Prerequisite{}

	for rows.Next() {
		prerequisite := &domain.Prerequisite{}

		if err := rows.Scan(
			&prerequisite.NodeID,
			&prerequisite.RequiresNodeID,
			&prerequisite.CreatedAt,
		); err != nil {
			return nil, err
		}

		prerequisites = append(prerequisites, prerequisite)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return prerequisites, nil
}
