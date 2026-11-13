package service

import (
	"context"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/Furry-Drama-Tracker/backend/internal/repository"
)

// PurgeUserData 完整删除用户及其个人数据（账号注销/管理员删号共用）。
// 采用 best-effort：单个集合清理失败不阻断后续步骤，最终以用户删除错误为准。
// 用户创建的剧集等公共内容保留（内容归属可能涉及其他协作者）。
func PurgeUserData(ctx context.Context, repos *repository.Repos, userID any) error {
	oid, _ := primitive.ObjectIDFromHex(toHex(userID))

	// 先取出评分影响的剧集，删除后重算平均分。
	var affected []primitive.ObjectID
	if oid != primitive.NilObjectID {
		if ratings, err := repos.Ratings.FindByUser(ctx, oid); err == nil {
			seen := map[string]bool{}
			for _, r := range ratings {
				if !seen[r.EpisodeID.Hex()] {
					seen[r.EpisodeID.Hex()] = true
					affected = append(affected, r.EpisodeID)
				}
			}
		}
	}

	_ = repos.Follows.DeleteByUser(ctx, userID)
	_ = repos.Histories.DeleteByUser(ctx, userID)
	_ = repos.Notifications.DeleteByUser(ctx, userID)
	_ = repos.Favorites.DeleteByUser(ctx, userID)
	_ = repos.Ratings.DeleteByUser(ctx, userID)
	_ = repos.Reports.DeleteByReporter(ctx, userID)
	_ = repos.Feedbacks.DeleteByUser(ctx, userID)
	_ = repos.Sessions.DeleteByUser(ctx, userID)
	_ = repos.Folders.AdminDeleteManyByUser(ctx, userID)
	_ = repos.SavedFolders.DeleteByUser(ctx, userID)
	_ = repos.CreatorProfiles.DeleteByCreator(ctx, userID)
	_ = repos.Users.AdminDeletePushSubscriptionsByUser(ctx, userID)
	// 认证因素清理：Passkey 凭据与外部身份绑定随账号一并删除
	// （loginnamereservations 的旧名保留记录按设计保留到期自动释放）。
	_ = repos.Passkeys.DeleteByUser(ctx, userID)
	_ = repos.ExternalIdents.DeleteByUser(ctx, userID)
	if oid != primitive.NilObjectID {
		_ = repos.Themes.DeleteByOwner(ctx, oid)
	}
	if len(affected) > 0 {
		_ = repos.Episodes.AdminRebuildEpisodeRatings(ctx, affected)
	}
	return repos.Users.DeleteByID(ctx, userID)
}

func toHex(id any) string {
	switch v := id.(type) {
	case string:
		return v
	case primitive.ObjectID:
		return v.Hex()
	case *primitive.ObjectID:
		if v != nil {
			return v.Hex()
		}
	}
	return ""
}
