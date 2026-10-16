package db

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// CategorySubtreeSQL selects the ids of the category whose id::text is $1
// and every category beneath it. A category filter means "this category
// and its subcategories": products are usually filed under a leaf
// (Cricket > Cricket Bats), so matching the parent's own id exactly
// returned nothing for "Cricket". Walks parent_id rather than the
// materialized path, which a rename leaves stale on descendants. Use it
// inside WithTenant as `p.category_id IN (`+db.CategorySubtreeSQL+`)`.
const CategorySubtreeSQL = `
	WITH RECURSIVE category_subtree AS (
		SELECT id FROM categories WHERE id::text = $1
		UNION
		SELECT child.id FROM categories child JOIN category_subtree s ON child.parent_id = s.id
	)
	SELECT id FROM category_subtree`

// CategorySubtree returns categoryID and the ids of all its descendants,
// for filters that can't run the SQL above themselves (OpenSearch). An
// unknown id yields an empty slice, so the filter matches nothing.
func CategorySubtree(ctx context.Context, tx pgx.Tx, categoryID string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT id::text FROM (`+CategorySubtreeSQL+`) t`, categoryID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
