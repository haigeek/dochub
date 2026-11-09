package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrTokenNameExists = errors.New("凭证名称已存在")

type AccessToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Token      string     `json:"token,omitempty"`
	Ciphertext string     `json:"-"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	CreatedAt  time.Time  `json:"createdAt"`
}

func (s *Store) CreateAccessToken(
	ctx context.Context,
	userID, name, tokenHash, tokenPrefix, tokenCiphertext string,
	expiresAt *time.Time,
) (AccessToken, error) {
	token := AccessToken{
		ID: newID(), Name: name, Prefix: tokenPrefix, Ciphertext: tokenCiphertext, ExpiresAt: expiresAt,
	}
	err := s.DB.QueryRow(ctx, `
		INSERT INTO access_tokens
			(id, user_id, name, token_hash, token_prefix, token_ciphertext, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id, name) DO NOTHING
		RETURNING created_at`,
		token.ID, userID, token.Name, tokenHash, token.Prefix, token.Ciphertext, token.ExpiresAt,
	).Scan(&token.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AccessToken{}, ErrTokenNameExists
	}
	return token, err
}

func (s *Store) ListAccessTokens(ctx context.Context, userID string) ([]AccessToken, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT id, name, token_prefix, token_ciphertext, expires_at, last_used_at, created_at
		FROM access_tokens
		WHERE user_id = $1
		ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tokens := make([]AccessToken, 0)
	for rows.Next() {
		token, err := scanAccessToken(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

func (s *Store) DeleteAccessToken(ctx context.Context, userID, tokenID string) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM access_tokens WHERE id = $1 AND user_id = $2`, tokenID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AccessTokenUser(
	ctx context.Context,
	tokenHash string,
) (User, error) {
	var user User
	var tokenID string
	err := s.DB.QueryRow(ctx, `
		SELECT u.id, u.username, u.role, u.created_at, COALESCE(u.password_hash, '') <> '',
		       t.id
		FROM access_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = $1
		  AND (t.expires_at IS NULL OR t.expires_at > NOW())`, tokenHash,
	).Scan(
		&user.ID, &user.Username, &user.Role, &user.CreatedAt, &user.HasPassword,
		&tokenID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	_, _ = s.DB.Exec(ctx, `UPDATE access_tokens SET last_used_at = NOW() WHERE id = $1`, tokenID)
	return user, nil
}

func scanAccessToken(row rowScanner) (AccessToken, error) {
	var token AccessToken
	err := row.Scan(
		&token.ID, &token.Name, &token.Prefix, &token.Ciphertext,
		&token.ExpiresAt, &token.LastUsedAt, &token.CreatedAt,
	)
	return token, err
}
