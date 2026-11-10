package api

import (
	"errors"
	"net/http"
	"strings"

	"dochub/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *Server) updateUser(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "INVALID_REQUEST", "用户参数不完整", "")
		return
	}
	input.Username = strings.TrimSpace(input.Username)
	if input.Username == "" || len(input.Username) > 64 || (input.Password != "" && (len(input.Password) < 8 || len(input.Password) > 72)) {
		fail(c, http.StatusBadRequest, "INVALID_USER", "用户名必填且不超过 64 字节，新密码须为 8 至 72 字节", "")
		return
	}
	user, err := s.db.UpdateUser(c, c.Param("id"), input.Username, input.Password, c.GetString(string(contextSID)))
	if errors.Is(err, store.ErrNotFound) {
		fail(c, http.StatusNotFound, "USER_NOT_FOUND", "用户不存在", "")
		return
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		fail(c, http.StatusConflict, "USERNAME_EXISTS", "用户名已存在", "")
		return
	}
	if err != nil {
		internal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": user})
}

func (s *Server) deleteUser(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	if c.Param("id") == currentUser(c).ID {
		fail(c, http.StatusBadRequest, "SELF_DELETE", "不能删除当前登录账号", "")
		return
	}
	err := s.db.DeleteUser(c, c.Param("id"), currentUser(c).ID)
	if errors.Is(err, store.ErrNotFound) {
		fail(c, http.StatusNotFound, "USER_NOT_FOUND", "用户不存在", "")
		return
	}
	if errors.Is(err, store.ErrLastAdmin) {
		fail(c, http.StatusConflict, "LAST_ADMIN", err.Error(), "")
		return
	}
	if err != nil {
		internal(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
