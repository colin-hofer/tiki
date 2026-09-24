package tiki

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func legacyDatabase(t *testing.T, version int) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	baseline, err := schemaFiles.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(baseline)); err != nil {
		t.Fatal(err)
	}
	for next := 3; next <= version; next++ {
		migration, err := schemaFiles.ReadFile(fmt.Sprintf("migrations/%03d.sql", next))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		t.Fatal(err)
	}
	return path, db
}

func TestMigrationsPreserveData(t *testing.T) {
	passwordHash := hashPassword(testPassword)
	token := randomToken()
	for _, version := range []int{2, 3, 4} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			path, db := legacyDatabase(t, version)
			if _, err := db.Exec("INSERT INTO users(id,name,email,password_hash,role) VALUES(7,'Admin','admin@example.test',?,'admin')", passwordHash); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("INSERT INTO sessions(user_id,hash,expires_at) VALUES(7,?,4102444800)", hashSession(token)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`
				INSERT INTO items(id,type,status,priority,title,created_by,created_at,updated_at,version)
				VALUES(12,'task','todo',1024,'Keep this ticket',7,'2026-09-24','2026-09-24',9);
				INSERT INTO item_assignees VALUES(12,7);
				INSERT INTO tags VALUES(3,'production');
				INSERT INTO item_tags VALUES(12,3);
				INSERT INTO activity(item_id,actor_id,kind,created_at,data) VALUES(12,7,'create','2026-09-24','{}');
			`); err != nil {
				t.Fatal(err)
			}
			if version >= 3 {
				if _, err := db.Exec(`INSERT INTO invites(hash,role,created_by,created_at,expires_at) VALUES('saved-invite','member',7,1,4102444800)`); err != nil {
					t.Fatal(err)
				}
			}
			db.Close()
			// Reopening twice also checks that migrations are not applied again.
			for range 2 {
				s, err := Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				var gotVersion int
				if err := s.read.QueryRow("PRAGMA user_version").Scan(&gotVersion); err != nil || gotVersion != schemaVersion {
					t.Fatalf("schema: %d, %v", gotVersion, err)
				}
				item, err := s.Get(t.Context(), 12)
				if err != nil || item.Title != "Keep this ticket" || item.Version != 9 || len(item.Assignees) != 1 || len(item.Tags) != 1 {
					t.Fatalf("lost ticket: %+v, %v", item, err)
				}
				if user, err := s.Authenticate(t.Context(), token); err != nil || user.ID != 7 {
					t.Fatalf("lost session: %+v, %v", user, err)
				}
				if _, err := s.Login(t.Context(), "admin@example.test", testPassword); err != nil {
					t.Fatalf("lost password: %v", err)
				}
				var activities, invites int
				if err := s.read.QueryRow("SELECT count(*) FROM activity").Scan(&activities); err != nil || activities != 1 {
					t.Fatalf("lost activity: %d, %v", activities, err)
				}
				if err := s.read.QueryRow("SELECT count(*) FROM invites").Scan(&invites); err != nil || (version >= 3 && invites != 1) {
					t.Fatalf("lost invite: %d, %v", invites, err)
				}
				before, err := s.Revision(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.write.Exec("UPDATE users SET name='Renamed' WHERE id=7"); err != nil {
					t.Fatal(err)
				}
				after, err := s.Revision(t.Context())
				if err != nil || after.Users != before.Users+1 {
					t.Fatalf("missing revision trigger: %+v -> %+v, %v", before, after, err)
				}
				s.Close()
			}
		})
	}
}

func TestMigrationFailureRollsBackWholeUpgrade(t *testing.T) {
	path, db := legacyDatabase(t, 2)
	// Force the second migration to fail after the first migration has run.
	if _, err := db.Exec("CREATE TABLE user_revision (existing_data TEXT); INSERT INTO user_revision VALUES('keep')"); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("accepted a broken migration")
	}
	var version, invites, removedColumn int
	var data string
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatalf("advanced schema on failure: %d, %v", version, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='invites'").Scan(&invites); err != nil || invites != 0 {
		t.Fatalf("first migration was not rolled back: %d, %v", invites, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM pragma_table_info('users') WHERE name='removed_at'").Scan(&removedColumn); err != nil || removedColumn != 0 {
		t.Fatalf("partial migration was not rolled back: %d, %v", removedColumn, err)
	}
	if err := db.QueryRow("SELECT existing_data FROM user_revision").Scan(&data); err != nil || data != "keep" {
		t.Fatalf("changed existing data: %q, %v", data, err)
	}
}

func TestConcurrentMigrations(t *testing.T) {
	path, db := legacyDatabase(t, 2)
	db.Close()
	var group sync.WaitGroup
	for range 3 {
		group.Go(func() {
			s, err := Open(path)
			if err != nil {
				t.Error(err)
				return
			}
			s.Close()
		})
	}
	group.Wait()
}
