package tiki

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

const priorityGap = 1024.0

func rebalance(ctx context.Context, tx *sql.Tx, actor ID) error {
	// ponytail: rare O(n) maintenance; replace with local-window rebalancing if measured write pauses warrant it.
	_, err := tx.ExecContext(ctx, `WITH ranks AS MATERIALIZED (
		SELECT id, row_number() OVER (ORDER BY priority,id)*1024.0 AS rank FROM items
	) UPDATE items SET priority=(SELECT rank FROM ranks WHERE ranks.id=items.id),version=version+1`)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE ordering SET generation=generation+1 WHERE id=1"); err != nil {
		return err
	}
	return event(ctx, tx, actor, nil, "ordering.reset", map[string]string{"reason": "float precision exhausted"})
}

func appendPriority(ctx context.Context, tx *sql.Tx, actor ID) (float64, error) {
	for range 2 {
		var max sql.NullFloat64
		if err := tx.QueryRowContext(ctx, "SELECT max(priority) FROM items").Scan(&max); err != nil {
			return 0, err
		}
		if !max.Valid {
			return priorityGap, nil
		}
		rank := max.Float64 + priorityGap
		if !math.IsInf(rank, 0) && rank > max.Float64 {
			return rank, nil
		}
		if err := rebalance(ctx, tx, actor); err != nil {
			return 0, err
		}
	}
	return 0, invalid("cannot allocate a finite priority")
}

func (s *Store) Move(ctx context.Context, actor, id ID, in MoveItem) (Item, error) {
	var out Item
	if in.Version <= 0 {
		return out, invalid("positive expected version required")
	}
	if (in.Before == 0) == (in.After == 0) || in.Before < 0 || in.After < 0 {
		return out, invalid("specify exactly one of before or after")
	}
	anchorID := in.Before
	if in.After != 0 {
		anchorID = in.After
	}
	if id == anchorID {
		return out, invalid("cannot move an item relative to itself")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var version int64
		err := tx.QueryRowContext(ctx, "SELECT version FROM items WHERE id=?", id).Scan(&version)
		if errors.Is(err, sql.ErrNoRows) {
			return missing()
		}
		if err != nil {
			return err
		}
		if version != in.Version {
			return conflict(version)
		}
		for range 2 {
			var anchor float64
			err := tx.QueryRowContext(ctx, "SELECT priority FROM items WHERE id=?", anchorID).Scan(&anchor)
			if errors.Is(err, sql.ErrNoRows) {
				return missing()
			}
			if err != nil {
				return err
			}
			query := "SELECT priority FROM items WHERE id<>? AND (priority,id)<(?,?) ORDER BY priority DESC,id DESC LIMIT 1"
			if in.After != 0 {
				query = "SELECT priority FROM items WHERE id<>? AND (priority,id)>(?,?) ORDER BY priority,id LIMIT 1"
			}
			var neighbor float64
			err = tx.QueryRowContext(ctx, query, id, anchor, anchorID).Scan(&neighbor)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			var rank float64
			var valid bool
			if errors.Is(err, sql.ErrNoRows) {
				rank = anchor - priorityGap
				valid = rank < anchor
				if in.After != 0 {
					rank = anchor + priorityGap
					valid = rank > anchor
				}
			} else {
				low, high := neighbor, anchor
				if in.After != 0 {
					low, high = anchor, neighbor
				}
				// Dividing first avoids overflow when endpoints have opposite signs.
				rank = low/2 + high/2
				valid = rank > low && rank < high
			}
			if valid && !math.IsNaN(rank) && !math.IsInf(rank, 0) {
				_, err = tx.ExecContext(ctx, "UPDATE items SET priority=?,version=version+1,updated_at=? WHERE id=?", rank, now(), id)
				if err != nil {
					return err
				}
				out, err = getItem(ctx, tx, id)
				if err != nil {
					return err
				}
				return event(ctx, tx, actor, id, "item.moved", map[string]any{"move": in, "priority": rank, "version": out.Version})
			}
			if err = rebalance(ctx, tx, actor); err != nil {
				return err
			}
		}
		return invalid("cannot allocate a finite priority")
	})
	return out, err
}
