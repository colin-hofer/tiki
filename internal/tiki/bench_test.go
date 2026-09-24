package tiki

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

// Seed in one transaction so setup does not measure fsync or password hashing.
// Every hundredth item has the rare tag and assignee, including near the tail.
func benchmarkStore(b *testing.B, size int) *Store {
	b.Helper()
	s, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	err = s.transaction(b.Context(), func(tx *sql.Tx) error {
		for _, statement := range []string{
			`INSERT INTO users(id,name,email,role,password_hash) VALUES(1,'Bench','bench@example.test','admin','unused')`,
			fmt.Sprintf(`WITH RECURSIVE n(id) AS (VALUES(1) UNION ALL SELECT id+1 FROM n WHERE id<%d)
				INSERT INTO items(id,type,status,priority,title,description,created_by,created_at,updated_at)
				SELECT id,'task',CASE WHEN id%%3=0 THEN 'todo' ELSE 'backlog' END,id*1024.0,'Benchmark item','A representative description.',1,'2026-01-01','2026-01-01' FROM n`, size),
			`INSERT INTO tags(id,name) VALUES(1,'common'),(2,'rare')`,
			`INSERT INTO item_tags SELECT id,1 FROM items`,
			`INSERT INTO item_tags SELECT id,2 FROM items WHERE id%100=0`,
			`INSERT INTO item_assignees SELECT id,1 FROM items WHERE id%100=0`,
		} {
			if _, err := tx.ExecContext(b.Context(), statement); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	return s
}

func BenchmarkRead(b *testing.B) {
	for _, size := range []int{10_000, 100_000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			s := benchmarkStore(b, size)
			b.Run("Get", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := s.Get(b.Context(), ID(size)); err != nil {
						b.Fatal(err)
					}
				}
			})
			for _, tc := range []struct {
				name   string
				filter Filter
			}{
				{"List", Filter{}},
				{"Status", Filter{Status: StatusTodo}},
				{"RareTag", Filter{Tags: []string{"rare"}}},
				{"TagsAndStatus", Filter{Tags: []string{"common", "rare"}, Status: StatusTodo}},
				{"Assignee", Filter{Assignee: 1}},
			} {
				b.Run(tc.name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						if _, err := s.List(b.Context(), tc.filter); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
			b.Run("ParallelList", func(b *testing.B) {
				b.ReportAllocs()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						if _, err := s.List(context.Background(), Filter{}); err != nil {
							b.Error(err)
						}
					}
				})
			})
		})
	}
}
