package service

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Furry-Drama-Tracker/backend/internal/auth"
	"github.com/Furry-Drama-Tracker/backend/internal/errors"
	"github.com/Furry-Drama-Tracker/backend/internal/model"
)

// DeletionResult 是注销申请状态。
type DeletionResult struct {
	RequestedAt time.Time
	DeleteAt    time.Time
}

// RequestDeletion POST /api/auth/request-deletion（protect）。
// 置 deletionRequestedAt + audit；必须再次校验当前密码，防止会话被盗后恶意注销。
func (s *AuthService) RequestDeletion(ctx context.Context, user *model.User, password string) (*DeletionResult, error) {
	if user.DeletionRequestedAt != nil {
		return nil, errors.New(400, "已提交过注销申请")
	}
	full, err := s.Repos.Users.FindByIDWithAuth(ctx, user.ID)
	if err != nil {
		return nil, errors.New(500, "服务器错误")
	}
	if !auth.Compare(full.Password, password) {
		_ = s.Repos.Users.IncLoginAttempts(ctx, user.ID)
		return nil, errors.New(400, "密码不正确")
	}
	_ = s.Repos.Users.ResetLoginAttempts(ctx, user.ID)
	now := time.Now()
	// 只定点写 deletionRequestedAt（勿整文档替换）：user 是中间件经 publicProjection
	// 加载的投影结构，Save 会把 password/twoFactorSecret/twoFactorBackupCodes/
	// loginAttempts/lockUntil 覆写成零值——凭据被静默销毁，2FA 用户再也无法登录
	// （也无法在宽限期内取消注销，宽限到期即被物理清除）。
	if err := s.Repos.Users.SetDeletionRequestedAt(ctx, user.ID, &now); err != nil {
		return nil, errors.New(500, "服务器错误")
	}
	user.DeletionRequestedAt = &now
	s.Audit(ctx, user, "ACCOUNT_DELETION_REQUESTED", "auth", "User requested account deletion", "")
	return &DeletionResult{RequestedAt: now, DeleteAt: now.Add(DeletionGraceDays * 24 * time.Hour)}, nil
}

// CancelDeletion POST /api/auth/cancel-deletion（protect）。
// 清除申请 + audit（流程固定）。
func (s *AuthService) CancelDeletion(ctx context.Context, user *model.User) error {
	if user.DeletionRequestedAt == nil {
		return errors.New(400, "没有注销申请")
	}
	// 同 RequestDeletion：只定点清除 deletionRequestedAt，不整文档替换。
	if err := s.Repos.Users.SetDeletionRequestedAt(ctx, user.ID, nil); err != nil {
		return errors.New(500, "服务器错误")
	}
	user.DeletionRequestedAt = nil
	s.Audit(ctx, user, "ACCOUNT_DELETION_CANCELLED", "auth", "User cancelled account deletion", "")
	return nil
}

// DeletionStatus GET /api/auth/deletion-status（protect，响应契约固定）。
func (s *AuthService) DeletionStatus(user *model.User) gin.H {
	if user.DeletionRequestedAt == nil {
		return gin.H{"requested": false}
	}
	return gin.H{
		"requested":           true,
		"deletionRequestedAt": user.DeletionRequestedAt,
		"deleteAt":            user.DeletionRequestedAt.Add(DeletionGraceDays * 24 * time.Hour),
	}
}
