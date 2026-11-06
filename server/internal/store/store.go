package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrNotFound   = errors.New("记录不存在")
	ErrSlugExists = errors.New("项目路径已存在")
)

type Store struct {
	DB *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{DB: pool}, nil
}

func (s *Store) Close() {
	s.DB.Close()
}

func (s *Store) Migrate(ctx context.Context) error {
	statements := []string{
		`CREATE EXTENSION IF NOT EXISTS pg_trgm`,
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL CHECK (role IN ('admin', 'member')),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			csrf_token TEXT NOT NULL,
			expires_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS projects (
			id TEXT PRIMARY KEY,
			slug TEXT,
			name TEXT NOT NULL,
			repo_url TEXT NOT NULL,
			doc_root TEXT NOT NULL,
			doc_roots JSONB NOT NULL DEFAULT '[]'::jsonb,
			owner_id TEXT NOT NULL REFERENCES users(id),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`ALTER TABLE projects ADD COLUMN IF NOT EXISTS slug TEXT`,
		`ALTER TABLE projects ADD COLUMN IF NOT EXISTS doc_roots JSONB`,
		`UPDATE projects SET doc_roots = jsonb_build_array(doc_root)
		 WHERE doc_roots IS NULL OR doc_roots = '[]'::jsonb`,
		`ALTER TABLE projects ALTER COLUMN doc_roots SET DEFAULT '[]'::jsonb`,
		`ALTER TABLE projects ALTER COLUMN doc_roots SET NOT NULL`,
		`CREATE TABLE IF NOT EXISTS project_branches (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			commit_sha TEXT NOT NULL DEFAULT '',
			snapshot_path TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'never',
			last_error TEXT NOT NULL DEFAULT '',
			last_synced_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (project_id, name)
		)`,
		`CREATE TABLE IF NOT EXISTS documents (
			branch_id TEXT NOT NULL REFERENCES project_branches(id) ON DELETE CASCADE,
			path TEXT NOT NULL,
			title TEXT NOT NULL,
			markdown TEXT NOT NULL,
			plain_text TEXT NOT NULL,
			PRIMARY KEY (branch_id, path)
		)`,
		`CREATE INDEX IF NOT EXISTS documents_search_idx ON documents
			USING GIN ((title || ' ' || path || ' ' || plain_text) gin_trgm_ops)`,
		`CREATE TABLE IF NOT EXISTS sync_jobs (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			branch_name TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
			error TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			finished_at TIMESTAMPTZ
		)`,
	}
	for _, statement := range statements {
		if _, err := s.DB.Exec(ctx, statement); err != nil {
			return fmt.Errorf("执行数据库迁移失败: %w", err)
		}
	}
	if err := s.backfillProjectSlugs(ctx); err != nil {
		return fmt.Errorf("回填项目路径失败: %w", err)
	}
	finalStatements := []string{
		`ALTER TABLE projects ALTER COLUMN slug SET NOT NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS projects_slug_unique_idx ON projects (slug)`,
	}
	for _, statement := range finalStatements {
		if _, err := s.DB.Exec(ctx, statement); err != nil {
			return fmt.Errorf("执行项目路径迁移失败: %w", err)
		}
	}
	return nil
}

func (s *Store) backfillProjectSlugs(ctx context.Context) error {
	rows, err := s.DB.Query(ctx,
		`SELECT id, repo_url FROM projects WHERE slug IS NULL OR slug = '' ORDER BY created_at`,
	)
	if err != nil {
		return err
	}
	type pendingProject struct {
		id      string
		repoURL string
	}
	var pending []pendingProject
	for rows.Next() {
		var item pendingProject
		if err := rows.Scan(&item.id, &item.repoURL); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(pending) == 0 {
		return nil
	}

	usedRows, err := s.DB.Query(ctx, `SELECT slug FROM projects WHERE slug IS NOT NULL AND slug <> ''`)
	if err != nil {
		return err
	}
	used := make(map[string]bool)
	for usedRows.Next() {
		var slug string
		if err := usedRows.Scan(&slug); err != nil {
			usedRows.Close()
			return err
		}
		used[slug] = true
	}
	if err := usedRows.Err(); err != nil {
		usedRows.Close()
		return err
	}
	usedRows.Close()

	for _, item := range pending {
		base, err := ProjectSlug(item.repoURL)
		if err != nil {
			base = "project-" + item.id[:min(6, len(item.id))]
		}
		slug := uniqueSlug(base, used)
		if _, err := s.DB.Exec(ctx, `UPDATE projects SET slug = $2 WHERE id = $1`, item.id, slug); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) EnsureAdmin(ctx context.Context, username, password string) error {
	var count int
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx,
		`INSERT INTO users (id, username, password_hash, role) VALUES ($1, $2, $3, 'admin')`,
		newID(), username, string(hash),
	)
	if err != nil {
		return err
	}
	return nil
}

func (s *Store) UserByUsername(ctx context.Context, username string) (User, string, error) {
	var user User
	var hash string
	err := s.DB.QueryRow(ctx,
		`SELECT id, username, role, created_at, password_hash FROM users WHERE username = $1`,
		strings.TrimSpace(username),
	).Scan(&user.ID, &user.Username, &user.Role, &user.CreatedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, "", ErrNotFound
	}
	return user, hash, err
}

func (s *Store) UserBySession(ctx context.Context, sessionID string) (User, string, error) {
	var user User
	var csrf string
	err := s.DB.QueryRow(ctx, `
		SELECT u.id, u.username, u.role, u.created_at, s.csrf_token, s.expires_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id = $1 AND s.expires_at > NOW()`, sessionID,
	).Scan(&user.ID, &user.Username, &user.Role, &user.CreatedAt, &csrf, new(time.Time))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, "", ErrNotFound
	}
	return user, csrf, err
}

func (s *Store) CreateSession(ctx context.Context, userID, sessionID, csrf string, expiresAt time.Time) error {
	_, err := s.DB.Exec(ctx,
		`INSERT INTO sessions (id, user_id, csrf_token, expires_at) VALUES ($1, $2, $3, $4)`,
		sessionID, userID, csrf, expiresAt,
	)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, sessionID)
	return err
}

func (s *Store) CleanupSessions(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= NOW()`)
	return err
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, username, role, created_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.ID, &user.Username, &user.Role, &user.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) CreateUser(ctx context.Context, username, password, role string) (User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return User{}, err
	}
	user := User{ID: newID(), Username: strings.TrimSpace(username), Role: role, CreatedAt: time.Now()}
	_, err = s.DB.Exec(ctx,
		`INSERT INTO users (id, username, password_hash, role) VALUES ($1, $2, $3, $4)`,
		user.ID, user.Username, string(hash), role,
	)
	return user, err
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT p.id, p.slug, p.name, p.repo_url, p.doc_roots, p.owner_id, u.username,
		       p.created_at, p.updated_at
		FROM projects p JOIN users u ON u.id = p.owner_id
		ORDER BY p.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []Project
	for rows.Next() {
		var project Project
		var docRootsRaw string
		if err := rows.Scan(
			&project.ID, &project.Slug, &project.Name, &project.RepoURL, &docRootsRaw,
			&project.OwnerID, &project.OwnerName, &project.CreatedAt, &project.UpdatedAt,
		); err != nil {
			return nil, err
		}
		project.DocRoots = decodeDocRoots(docRootsRaw)
		project.Branches, err = s.ListBranches(ctx, project.ID)
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func (s *Store) Project(ctx context.Context, identifier string) (Project, error) {
	var project Project
	var docRootsRaw string
	err := s.DB.QueryRow(ctx, `
		SELECT p.id, p.slug, p.name, p.repo_url, p.doc_roots, p.owner_id, u.username,
		       p.created_at, p.updated_at
		FROM projects p JOIN users u ON u.id = p.owner_id
		WHERE p.id = $1 OR p.slug = $1
		ORDER BY CASE WHEN p.id = $1 THEN 0 ELSE 1 END
		LIMIT 1`, identifier,
	).Scan(
		&project.ID, &project.Slug, &project.Name, &project.RepoURL, &docRootsRaw,
		&project.OwnerID, &project.OwnerName, &project.CreatedAt, &project.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	project.DocRoots = decodeDocRoots(docRootsRaw)
	project.Branches, err = s.ListBranches(ctx, project.ID)
	return project, err
}

func (s *Store) CreateProject(
	ctx context.Context,
	name, repoURL string,
	docRoots []string,
	ownerID string,
	branches []string,
) (Project, error) {
	slug, err := ProjectSlug(repoURL)
	if err != nil {
		return Project{}, err
	}
	encodedRoots, err := encodeDocRoots(docRoots)
	if err != nil {
		return Project{}, err
	}
	project := Project{
		ID:       newID(),
		Slug:     slug,
		Name:     name,
		RepoURL:  repoURL,
		DocRoots: docRoots,
		OwnerID:  ownerID,
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return Project{}, err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM projects WHERE slug = $1)`, slug).Scan(&exists); err != nil {
		return Project{}, err
	}
	if exists {
		return Project{}, ErrSlugExists
	}
	if _, err = tx.Exec(ctx,
		`INSERT INTO projects (id, slug, name, repo_url, doc_root, doc_roots, owner_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		project.ID, slug, name, repoURL, docRoots[0], encodedRoots, ownerID,
	); err != nil {
		return Project{}, err
	}
	for _, branch := range branches {
		if _, err = tx.Exec(ctx,
			`INSERT INTO project_branches (id, project_id, name) VALUES ($1, $2, $3)`,
			newID(), project.ID, branch,
		); err != nil {
			return Project{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Project{}, err
	}
	return s.Project(ctx, project.ID)
}

func (s *Store) UpdateProject(ctx context.Context, id, name, repoURL string, docRoots []string) error {
	encodedRoots, err := encodeDocRoots(docRoots)
	if err != nil {
		return err
	}
	result, err := s.DB.Exec(ctx, `
		UPDATE projects
		SET name = $2, repo_url = $3, doc_root = $4, doc_roots = $5, updated_at = NOW()
		WHERE id = $1`, id, name, repoURL, docRoots[0], encodedRoots)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) InvalidateProject(ctx context.Context, projectID string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `
		DELETE FROM documents
		WHERE branch_id IN (SELECT id FROM project_branches WHERE project_id = $1)`,
		projectID,
	); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE project_branches
		SET status = 'never', commit_sha = '', snapshot_path = '', last_error = '',
		    last_synced_at = NULL
		WHERE project_id = $1`,
		projectID,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteProject(ctx context.Context, id string) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT snapshot_path FROM project_branches WHERE project_id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	result, err := s.DB.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	if result.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return paths, nil
}

func (s *Store) ListBranches(ctx context.Context, projectID string) ([]Branch, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id, project_id, name, commit_sha, status, last_error, last_synced_at
		FROM project_branches WHERE project_id = $1 ORDER BY name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var branches []Branch
	for rows.Next() {
		var branch Branch
		if err := rows.Scan(
			&branch.ID, &branch.ProjectID, &branch.Name, &branch.CommitSHA,
			&branch.Status, &branch.LastError, &branch.LastSyncedAt,
		); err != nil {
			return nil, err
		}
		branches = append(branches, branch)
	}
	return branches, rows.Err()
}

func (s *Store) AddBranch(ctx context.Context, projectID, name string) (Branch, error) {
	branch := Branch{ID: newID(), ProjectID: projectID, Name: name, Status: "never"}
	_, err := s.DB.Exec(ctx,
		`INSERT INTO project_branches (id, project_id, name) VALUES ($1, $2, $3)`,
		branch.ID, projectID, name,
	)
	return branch, err
}

func (s *Store) DeleteBranch(ctx context.Context, projectID, name string) (string, error) {
	var snapshot string
	err := s.DB.QueryRow(ctx,
		`DELETE FROM project_branches WHERE project_id = $1 AND name = $2 RETURNING snapshot_path`,
		projectID, name,
	).Scan(&snapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return snapshot, err
}

func (s *Store) BranchSnapshot(ctx context.Context, projectID, name string) (string, []string, error) {
	var snapshot, docRootsRaw string
	err := s.DB.QueryRow(ctx, `
		SELECT pb.snapshot_path, p.doc_roots
		FROM project_branches pb JOIN projects p ON p.id = pb.project_id
		WHERE pb.project_id = $1 AND pb.name = $2`, projectID, name,
	).Scan(&snapshot, &docRootsRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrNotFound
	}
	return snapshot, decodeDocRoots(docRootsRaw), err
}

func (s *Store) MarkBranchRunning(ctx context.Context, projectID, branch string) error {
	_, err := s.DB.Exec(ctx, `
		UPDATE project_branches SET status = 'running', last_error = ''
		WHERE project_id = $1 AND name = $2`, projectID, branch)
	return err
}

func (s *Store) FinishBranchSync(ctx context.Context, projectID, branch, commit, snapshot string, docs []DocFile) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var branchID string
	if err = tx.QueryRow(ctx,
		`UPDATE project_branches SET commit_sha = $3, snapshot_path = $4, status = 'succeeded',
		 last_error = '', last_synced_at = NOW()
		 WHERE project_id = $1 AND name = $2 RETURNING id`,
		projectID, branch, commit, snapshot,
	).Scan(&branchID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM documents WHERE branch_id = $1`, branchID); err != nil {
		return err
	}
	for _, doc := range docs {
		if _, err = tx.Exec(ctx, `
			INSERT INTO documents (branch_id, path, title, markdown, plain_text)
			VALUES ($1, $2, $3, $4, $5)`,
			branchID, doc.Path, doc.Title, doc.Markdown, doc.PlainText,
		); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE projects SET updated_at = NOW() WHERE id = $1`, projectID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) FailBranchSync(ctx context.Context, projectID, branch, message string) error {
	_, err := s.DB.Exec(ctx, `
		UPDATE project_branches SET status = 'failed', last_error = $3
		WHERE project_id = $1 AND name = $2`, projectID, branch, message)
	return err
}

func (s *Store) ListDocuments(ctx context.Context, projectID, branch string) ([]Document, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT d.path, d.title
		FROM documents d JOIN project_branches pb ON pb.id = d.branch_id
		WHERE pb.project_id = $1 AND pb.name = $2
		ORDER BY d.path`, projectID, branch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var docs []Document
	for rows.Next() {
		var doc Document
		if err := rows.Scan(&doc.Path, &doc.Title); err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

func (s *Store) Document(ctx context.Context, projectID, branch, docPath string) (DocFile, error) {
	var doc DocFile
	err := s.DB.QueryRow(ctx, `
		SELECT d.path, d.title, d.markdown, d.plain_text
		FROM documents d JOIN project_branches pb ON pb.id = d.branch_id
		WHERE pb.project_id = $1 AND pb.name = $2 AND d.path = $3`,
		projectID, branch, docPath,
	).Scan(&doc.Path, &doc.Title, &doc.Markdown, &doc.PlainText)
	if errors.Is(err, pgx.ErrNoRows) {
		return DocFile{}, ErrNotFound
	}
	return doc, err
}

func (s *Store) SearchDocuments(ctx context.Context, projectID, branch, query string, limit int) ([]SearchResult, error) {
	pattern := "%" + query + "%"
	rows, err := s.DB.Query(ctx, `
		SELECT d.path, d.title,
		       substring(d.plain_text from greatest(1, position(lower($3) in lower(d.plain_text)) - 50) for 180)
		FROM documents d JOIN project_branches pb ON pb.id = d.branch_id
		WHERE pb.project_id = $1 AND pb.name = $2
		  AND (d.title ILIKE $4 OR d.path ILIKE $4 OR d.plain_text ILIKE $4)
		ORDER BY d.title LIMIT $5`,
		projectID, branch, query, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []SearchResult
	for rows.Next() {
		var result SearchResult
		if err := rows.Scan(&result.Path, &result.Title, &result.Excerpt); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func (s *Store) CreateJob(ctx context.Context, projectID, branch string) (SyncJob, error) {
	job := SyncJob{ID: newID(), ProjectID: projectID, Branch: branch, Status: "queued", CreatedAt: time.Now()}
	_, err := s.DB.Exec(ctx,
		`INSERT INTO sync_jobs (id, project_id, branch_name, status) VALUES ($1, $2, $3, 'queued')`,
		job.ID, projectID, branch,
	)
	return job, err
}

func (s *Store) Job(ctx context.Context, id string) (SyncJob, error) {
	var job SyncJob
	err := s.DB.QueryRow(ctx, `
		SELECT id, project_id, branch_name, status, error, created_at, finished_at
		FROM sync_jobs WHERE id = $1`, id,
	).Scan(&job.ID, &job.ProjectID, &job.Branch, &job.Status, &job.Error, &job.CreatedAt, &job.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SyncJob{}, ErrNotFound
	}
	return job, err
}

func (s *Store) StartJob(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `UPDATE sync_jobs SET status = 'running' WHERE id = $1`, id)
	return err
}

func (s *Store) FinishJob(ctx context.Context, id, status, message string) error {
	_, err := s.DB.Exec(ctx, `
		UPDATE sync_jobs SET status = $2, error = $3, finished_at = NOW() WHERE id = $1`,
		id, status, message)
	return err
}

func (s *Store) FailInterruptedJobs(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `
		UPDATE sync_jobs SET status = 'failed', error = '服务重启，任务未完成', finished_at = NOW()
		WHERE status IN ('queued', 'running')`)
	return err
}

// encodeDocRoots 使用 JSON 存储多个目录，同时保留原有单目录文本数据的兼容性。
func encodeDocRoots(docRoots []string) (string, error) {
	if len(docRoots) == 0 {
		return "", fmt.Errorf("至少需要一个文档目录")
	}
	value, err := json.Marshal(docRoots)
	if err != nil {
		return "", fmt.Errorf("编码文档目录失败: %w", err)
	}
	return string(value), nil
}

func decodeDocRoots(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var docRoots []string
	if strings.HasPrefix(raw, "[") && json.Unmarshal([]byte(raw), &docRoots) == nil {
		return docRoots
	}
	return []string{raw}
}

func newID() string {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value)
}
