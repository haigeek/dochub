package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"dochub/internal/store"
)

func (s *Server) listAccessTokens(c *gin.Context) {
	tokens, err := s.db.ListAccessTokens(c, currentUser(c).ID)
	if err != nil {
		internal(c, err)
		return
	}
	for index := range tokens {
		plaintext, err := s.secrets.Decrypt(tokens[index].Ciphertext)
		if err != nil {
			internal(c, err)
			return
		}
		tokens[index].Ciphertext = ""
		tokens[index].Token = plaintext
	}
	c.JSON(http.StatusOK, gin.H{"data": tokens})
}

func (s *Server) createAccessToken(c *gin.Context) {
	var input struct {
		Name          string `json:"name"`
		ExpiresInDays *int   `json:"expiresInDays"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_TOKEN", "凭证参数无效", "")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len([]rune(input.Name)) > 80 {
		fail(c, http.StatusBadRequest, "INVALID_TOKEN", "凭证名称应为 1-80 个字符", "")
		return
	}
	var expiresAt *time.Time
	if input.ExpiresInDays != nil && *input.ExpiresInDays > 0 {
		if *input.ExpiresInDays > 1825 {
			fail(c, http.StatusBadRequest, "INVALID_TOKEN", "凭证有效期不能超过 5 年", "")
			return
		}
		expiry := time.Now().Add(time.Duration(*input.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &expiry
	}
	rawToken := "dochub_pat_" + randomToken(32)
	ciphertext, err := s.secrets.Encrypt(rawToken)
	if err != nil {
		internal(c, err)
		return
	}
	token, err := s.db.CreateAccessToken(
		c, currentUser(c).ID, input.Name, hashToken(rawToken), rawToken[:19], ciphertext,
		expiresAt,
	)
	if errors.Is(err, store.ErrTokenNameExists) {
		fail(c, http.StatusConflict, "TOKEN_NAME_EXISTS", "凭证名称已存在", "")
		return
	}
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": gin.H{
		"id": token.ID, "name": token.Name, "prefix": token.Prefix,
		"expiresAt":  token.ExpiresAt,
		"lastUsedAt": token.LastUsedAt, "createdAt": token.CreatedAt,
		"token": rawToken,
	}})
}

func (s *Server) deleteAccessToken(c *gin.Context) {
	if err := s.db.DeleteAccessToken(c, currentUser(c).ID, c.Param("tokenId")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(c, http.StatusNotFound, "TOKEN_NOT_FOUND", "Agent 凭证不存在", "")
			return
		}
		internal(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
