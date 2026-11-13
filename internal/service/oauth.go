package service

import (
	"context"
	"errors"
)

// OAuthProvider 是外部身份提供方的抽象（QQ 互联预留）。
// 上线 QQ 时实现 qqProvider 并在注册路由处启用；本期仅定义契约与占位实现。
// GetAuthURL 返回授权跳转地址与防 CSRF 的 state（调用方需把 state 与会话绑定）。
// ExchangeCode 用授权码换取 openid/unionid（unionid 需同一开放平台主体才返回）。
type OAuthProvider interface {
	Provider() string
	Enabled() bool
	GetAuthURL(ctx context.Context, state string) (string, error)
	ExchangeCode(ctx context.Context, code string) (openid, unionid string, err error)
}

// disabledProvider 是未配置 provider 的占位实现：所有调用返回未启用错误
//（路由层据此返回 501）。
type disabledProvider struct{ name string }

func (p disabledProvider) Provider() string { return p.name }
func (p disabledProvider) Enabled() bool    { return false }
func (p disabledProvider) GetAuthURL(_ context.Context, _ string) (string, error) {
	return "", ErrOAuthDisabled
}
func (p disabledProvider) ExchangeCode(_ context.Context, _ string) (string, string, error) {
	return "", "", ErrOAuthDisabled
}

// ErrOAuthDisabled 标记 OAuth 提供方未启用。
var ErrOAuthDisabled = errors.New("oauth provider not enabled")

// NewQQProvider 构造 QQ 互联 provider。
// 本期为预留 stub：QQ_APPID/QQ_APPKEY 未配置时返回 disabled 实现；
// 上线时在此接入 https://graph.qq.com/oauth2.0/... 的 authorize/token/openid_get。
func NewQQProvider(appID, appKey string) OAuthProvider {
	if appID == "" || appKey == "" {
		return disabledProvider{name: "qq"}
	}
	// TODO(QQ 互联上线)：实现 qqProvider{appID, appKey}：
	//   GetAuthURL  → https://graph.qq.com/oauth2.0/authorize?response_type=code&client_id=...&state=...
	//   ExchangeCode→ POST /oauth2.0/token 换 access_token，GET /oauth2.0/me 换 openid，
	//                 GET /user/get_user_info（可选，头像昵称）；unionid 需开放平台资质。
	return disabledProvider{name: "qq"}
}
