package sqlite

import (
	"database/sql"
	"fmt"
	"jakegodsall/knowledge-tree/src/domain"

	"github.com/google/uuid"
)

type NodeTagRepository struct {
	db *sql.DB
}

func NewNodeTagRepository(db *sql.DB) *NodeTagRepository {
	return &NodeTagRepository{
		db: db,
	}
}

func (r *NodeTagRepository) Create(tag *domain.NodeTag) error {
	res, err := r.db.Exec(`
			INSERT INTO node_tags (node_id, tag, created_at)
			VALUES (?, ?, ?)
		`,
		tag.NodeID,
		tag.Tag,
		tag.CreatedAt,
	)

	if isDuplicate(err) {
		return fmt.Errorf("node %s is already tagged %s", tag.NodeID.String(), tag.Tag)
	}

	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()

	if err != nil {
		return err
	}

	if rows == 0 {
		return fmt.Errorf("could not tag node %s with %s", tag.NodeID.String(), tag.Tag)
	}

	return nil
}

func (r *NodeTagRepository) GetAll() ([]*domain.NodeTag, error) {
	return r.query(`
			SELECT node_id, tag, created_at
			FROM node_tags
			ORDER BY tag
		`)
}

func (r *NodeTagRepository) FindByNode(nodeID uuid.UUID) ([]*domain.NodeTag, error) {
	return r.query(`
			SELECT node_id, tag, created_at
			FROM node_tags
			WHERE node_id = ?
			ORDER BY tag
		`,
		nodeID,
	)
}

func (r *NodeTagRepository) FindByTag(tag string) ([]*domain.NodeTag, error) {
	return r.query(`
			SELECT node_id, tag, created_at
			FROM node_tags
			WHERE tag = ?
		`,
		domain.NormaliseTag(tag),
	)
}

func (r *NodeTagRepository) Delete(nodeID uuid.UUID, tag string) error {
	tag = domain.NormaliseTag(tag)

	res, err := r.db.Exec(
		"DELETE FROM node_tags WHERE node_id = ? AND tag = ?",
		nodeID,
		tag,
	)

	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()

	if err != nil {
		return err
	}

	if rows == 0 {
		return fmt.Errorf("tag %s on node %s could not be deleted", tag, nodeID.String())
	}

	return nil
}

func (r *NodeTagRepository) query(query string, args ...any) ([]*domain.NodeTag, error) {
	rows, err := r.db.Query(query, args...)

	if err != nil {
		return nil, err
	}

	defer rows.Close()

	tags := []*domain.NodeTag{}

	for rows.Next() {
		tag := &domain.NodeTag{}

		if err := rows.Scan(
			&tag.NodeID,
			&tag.Tag,
			&tag.CreatedAt,
		); err != nil {
			return nil, err
		}

		tags = append(tags, tag)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return tags, nil
}
