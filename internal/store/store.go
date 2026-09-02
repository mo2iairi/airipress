package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type Store struct {
	*sql.DB
	Postgres bool
}

type Tx struct {
	*sql.Tx
	Postgres bool
}

func (s *Store) Begin() (*Tx, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	return &Tx{Tx: tx, Postgres: s.Postgres}, nil
}
func (t *Tx) Exec(query string, args ...any) (sql.Result, error) {
	return t.Tx.Exec(bind(query, t.Postgres), args...)
}
func (t *Tx) Query(query string, args ...any) (*sql.Rows, error) {
	return t.Tx.Query(bind(query, t.Postgres), args...)
}

var qmark = regexp.MustCompile(`\?`)

func bind(query string, postgres bool) string {
	if !postgres {
		return query
	}
	i := 0
	return qmark.ReplaceAllStringFunc(query, func(string) string {
		i++
		return fmt.Sprintf("$%d", i)
	})
}

func (s *Store) Exec(query string, args ...any) (sql.Result, error) {
	return s.DB.Exec(bind(query, s.Postgres), args...)
}

func (s *Store) Query(query string, args ...any) (*sql.Rows, error) {
	return s.DB.Query(bind(query, s.Postgres), args...)
}

func (s *Store) QueryRow(query string, args ...any) *sql.Row {
	return s.DB.QueryRow(bind(query, s.Postgres), args...)
}

func Open(dsn string) (*Store, error) {
	if dsn == "" {
		dsn = "file:.meta/airipress.db?_pragma=foreign_keys(1)"
	}
	if strings.HasPrefix(dsn, "file:") {
		p := strings.TrimPrefix(dsn, "file:")
		if i := strings.IndexByte(p, '?'); i >= 0 {
			p = p[:i]
		}
		if p != ":memory:" && p != "" {
			if err := os.MkdirAll(filepath.Dir(p), 0750); err != nil {
				return nil, err
			}
		}
	}
	postgres := strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://")
	driver := "sqlite"
	if postgres {
		driver = "pgx"
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{DB: db, Postgres: postgres}
	if !postgres {
		if _, err = db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS workspaces(id TEXT PRIMARY KEY,name TEXT NOT NULL,root_path TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS models(id TEXT PRIMARY KEY,name TEXT NOT NULL,provider TEXT NOT NULL,model TEXT NOT NULL,api_key TEXT NOT NULL DEFAULT '',base_url TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS files(id TEXT PRIMARY KEY,sha256 TEXT UNIQUE NOT NULL,name TEXT NOT NULL,mime TEXT NOT NULL,size BIGINT NOT NULL,object_key TEXT NOT NULL,created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS sources(id TEXT PRIMARY KEY,workspace_id TEXT NOT NULL,file_id TEXT NOT NULL,relative_path TEXT NOT NULL,source_type TEXT NOT NULL DEFAULT 'upload',created_at TEXT NOT NULL,UNIQUE(workspace_id,relative_path),FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,FOREIGN KEY(file_id) REFERENCES files(id))`,
		`CREATE TABLE IF NOT EXISTS chats(id TEXT PRIMARY KEY,workspace_id TEXT NOT NULL,title TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS messages(id TEXT PRIMARY KEY,chat_id TEXT NOT NULL,role TEXT NOT NULL,content TEXT NOT NULL,created_at TEXT NOT NULL,FOREIGN KEY(chat_id) REFERENCES chats(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS message_versions(id TEXT PRIMARY KEY,message_id TEXT NOT NULL,content TEXT NOT NULL,is_selected BOOLEAN NOT NULL DEFAULT FALSE,created_at TEXT NOT NULL,FOREIGN KEY(message_id) REFERENCES messages(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS site_themes(id TEXT PRIMARY KEY,name TEXT NOT NULL,engine TEXT NOT NULL,repository TEXT NOT NULL,ref TEXT NOT NULL DEFAULT '',preview_url TEXT NOT NULL DEFAULT '',description TEXT NOT NULL DEFAULT '',git_commit TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS mindmaps(id TEXT PRIMARY KEY,workspace_id TEXT NOT NULL UNIQUE,content TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS deploy_jobs(id TEXT PRIMARY KEY,workspace_id TEXT NOT NULL,status TEXT NOT NULL,config TEXT NOT NULL DEFAULT '',url TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,FOREIGN KEY(workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS github_oauth_accounts(id INTEGER PRIMARY KEY,login TEXT NOT NULL,access_token TEXT NOT NULL,scopes TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL)`,
	}
	for _, statement := range schema {
		if _, err := s.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
