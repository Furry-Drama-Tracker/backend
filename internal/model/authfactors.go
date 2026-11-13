package model

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ExternalIdentity 是外部身份提供方的绑定记录（externalidentities 集合）。
// provider 目前仅预留 "qq"（QQ 互联），上线前该集合不会有数据。
// (provider, openid) 唯一索引；unionid 保留以支持跨应用统一身份。
type ExternalIdentity struct {
	ID       primitive.ObjectID `bson:"_id,omitempty" json:"_id"`
	UserID   primitive.ObjectID `bson:"userId" json:"userId"`
	UID      int64              `bson:"uid" json:"uid"`
	Provider string             `bson:"provider" json:"provider"` // "qq"（预留）
	OpenID   string             `bson:"openid" json:"-"`
	UnionID  string             `bson:"unionid,omitempty" json:"-"`
	LinkedAt time.Time          `bson:"linkedAt" json:"linkedAt"`
	CreatedAt time.Time         `bson:"createdAt" json:"createdAt"`
}

// Passkey 是一条 WebAuthn 凭据（passkeys 集合）。
// WebID（WebAuthn user handle）= UID 的大端 8 字节编码，绑定在用户侧而非凭据侧。
type Passkey struct {
	ID primitive.ObjectID `bson:"_id,omitempty" json:"_id"`
	// CredentialID 是 WebAuthn credential ID 的 base64url 编码（凭据唯一标识）。
	CredentialID string `bson:"credentialId" json:"credentialId"`
	// UserID 是 users._id（ObjectID）， Passkey.UserIDHex 对外。
	UserID    primitive.ObjectID `bson:"userId" json:"userId"`
	UID       int64              `bson:"uid" json:"uid"`
	Name      string             `bson:"name" json:"name"` // 用户自定义设备名
	PublicKey string             `bson:"publicKey" json:"-"` // COSE 公钥的 base64url
	// AttestationType 是凭据的 attestation 格式（通常 "none"）。
	AttestationType string `bson:"attestationType" json:"attestationType"`
	// Transport 是凭据支持的传输方式（internal/hybrid/usb/nfc/ble）。
	Transport []string `bson:"transport,omitempty" json:"transport,omitempty"`
	// SignCount 是签名计数器（凭据克隆检测）。
	SignCount uint32 `bson:"signCount" json:"-"`
	// UVE 标记凭据是否支持 UV（用户验证）且上次仪式已通过。
	UVE bool `bson:"backupEligible" json:"backupEligible"`
	BackupState bool `bson:"backupState" json:"backupState"`
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	LastUsedAt time.Time `bson:"lastUsedAt" json:"lastUsedAt"`
}
