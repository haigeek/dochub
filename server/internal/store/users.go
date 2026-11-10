package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

func (s *Store) UpdateUser(ctx context.Context, userID, username, password, currentSessionID string) (User, error) {
	var hash string
	if password != "" {
		value, err := bcrypt.GenerateFromPassword([]byte(password), 12)
		if err != nil {
			return User{}, err
		}
		hash = string(value)
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)
	var user User
	err = tx.QueryRow(ctx, `UPDATE users SET username = $2,
 password_hash = CASE WHEN $3 = '' THEN password_hash ELSE $3 END
 WHERE id = $1 RETURNING id, username, role, COALESCE(password_hash, '') <> '', created_at`,
		userID, username, hash).Scan(&user.ID, &user.Username, &user.Role, &user.HasPassword, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	// 重置密码后撤销旧会话，编辑自己的账号时保留当前会话。
	if hash != "" {
		if _, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND id <> $2`, userID, currentSessionID); err != nil {
			return User{}, err
		}
	}
	return user, tx.Commit(ctx)
}

func (s *Store) DeleteUser(ctx context.Context, userID, successorID string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// 与角色调整共用事务锁，防止并发删除或降级导致没有管理员。
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(73429012)`); err != nil {
		return err
	}
	var role string
	err = tx.QueryRow(ctx, `SELECT role FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if role == "admin" {
		var count int
		if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&count); err != nil {
			return err
		}
		if count <= 1 {
			return ErrLastAdmin
		}
	}
	// 保留项目和已有授权，由执行删除的管理员接管其管理记录。
	for _, query := range []string{
		`UPDATE projects SET owner_id = $2 WHERE owner_id = $1`,
		`UPDATE project_read_permissions SET created_by = $2 WHERE created_by = $1`,
		`UPDATE document_read_permissions SET created_by = $2 WHERE created_by = $1`,
		`UPDATE project_read_organizations SET created_by = $2 WHERE created_by = $1`,
		`UPDATE document_read_organizations SET created_by = $2 WHERE created_by = $1`,
	} {
		if _, err = tx.Exec(ctx, query, userID, successorID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
