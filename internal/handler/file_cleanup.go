package handler

import (
	"context"
	"strings"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/xiedada05/furry-drama-be-neo/internal/repository"
	"github.com/xiedada05/furry-drama-be-neo/internal/upload"
)

// uploadedFileInUse 判断 /uploads 文件是否仍被主题、系统壁纸或用户个人壁纸引用。
// excludeThemeID 用于删除主题时排除自身引用。
func uploadedFileInUse(ctx context.Context, repos *repository.Repos, url string, excludeThemeID primitive.ObjectID) bool {
	if n, err := repos.Themes.CountURLReferences(ctx, url, excludeThemeID); err == nil && n > 0 {
		return true
	}
	if ok, err := repos.Wallpapers.ExistsByURL(ctx, url); err == nil && ok {
		return true
	}
	if ok, err := repos.Users.PersonalWallpaperInUse(ctx, url); err == nil && ok {
		return true
	}
	return false
}

// removeUploadedFileIfUnused 仅在文件无业务引用且文件名为系统生成的安全前缀时删除。
// prefix 用于限制只能删除本业务上传的文件，防止数据库被污染后误删头像/封面等。
func removeUploadedFileIfUnused(ctx context.Context, repos *repository.Repos, url, prefix string, excludeThemeID primitive.ObjectID) {
	if !strings.HasPrefix(url, "/uploads/"+prefix) {
		return
	}
	if uploadedFileInUse(ctx, repos, url, excludeThemeID) {
		return
	}
	_ = upload.RemoveFile(strings.TrimPrefix(url, "/uploads/"))
}
