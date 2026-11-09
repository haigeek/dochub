package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
)

type OAuthLogin struct {
	ReturnPath string
	Verifier   string
}

func (s *Store) CreateOAuthLogin(ctx context.Context, stateHash, providerID, verifier, returnPath string, expiresAt time.Time) error {
	if _, err := s.DB.Exec(ctx, `DELETE FROM oauth_login_transactions WHERE expires_at <= NOW()`); err != nil {
		return err
	}
	_, err := s.DB.Exec(ctx, `
		INSERT INTO oauth_login_transactions (state_hash, provider_id, verifier, return_path, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, stateHash, providerID, verifier, returnPath, expiresAt)
	return err
}

func (s *Store) ConsumeOAuthLogin(ctx context.Context, stateHash, providerID string) (OAuthLogin, error) {
	var transaction OAuthLogin
	err := s.DB.QueryRow(ctx, `
		DELETE FROM oauth_login_transactions
		WHERE state_hash = $1 AND provider_id = $2 AND expires_at > NOW()
		RETURNING return_path, verifier`, stateHash, providerID).Scan(&transaction.ReturnPath, &transaction.Verifier)
	if errors.Is(err, pgx.ErrNoRows) {
		return OAuthLogin{}, ErrNotFound
	}
	return transaction, err
}

func (s *Store) OAuthUser(ctx context.Context, providerID, issuer, subject, displayName string) (User, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)

	identity, _ := json.Marshal([]string{providerID, issuer, subject})
	sum := sha256.Sum256(identity)
	// 在 Go 中生成稳定锁 ID，兼容没有 hashtextextended 的数据库；
	// 同一外部身份并发首次登录时串行建号，避免产生没有身份绑定的孤立账号。
	lockID := int64(binary.BigEndian.Uint64(sum[:8]))
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1::bigint)`, lockID); err != nil {
		return User{}, err
	}
	var user User
	err = tx.QueryRow(ctx, `
		SELECT u.id, u.username, u.role, u.created_at, COALESCE(u.password_hash, '') <> ''
		FROM oauth_identities ci JOIN users u ON u.id = ci.user_id
		WHERE ci.provider_id = $1 AND ci.issuer = $2 AND ci.subject = $3`, providerID, issuer, subject).
		Scan(&user.ID, &user.Username, &user.Role, &user.CreatedAt, &user.HasPassword)
	if err == nil {
		return user, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return User{}, err
	}

	user = User{ID: newID(), Role: "member"}
	base := strings.TrimSpace(strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, displayName))
	baseRunes := []rune(base)
	if len(baseRunes) > 32 {
		base = string(baseRunes[:32])
	}
	if base == "" {
		base = providerID
	}
	user.Username = base
	for attempt := 0; ; attempt++ {
		err = tx.QueryRow(ctx, `
			INSERT INTO users (id, username, password_hash, role)
			VALUES ($1, $2, NULL, 'member')
			ON CONFLICT (username) DO NOTHING
			RETURNING created_at`, user.ID, user.Username).Scan(&user.CreatedAt)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return User{}, err
		}
		// 由唯一约束处理并发重名，冲突后按 _1、_2 依次寻找可用登录名。
		user.Username = base + "_" + strconv.Itoa(attempt+1)
	}
	// 不按名称认领已有账号，用户角色和项目权限始终由 DocHub 本地决定。
	if _, err = tx.Exec(ctx, `
		INSERT INTO oauth_identities (provider_id, issuer, subject, user_id) VALUES ($1, $2, $3, $4)`,
		providerID, issuer, subject, user.ID); err != nil {
		return User{}, err
	}
	return user, tx.Commit(ctx)
}
