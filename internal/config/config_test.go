package config

import "testing"

func TestValidateKeepsLegacyProductionConfigRunning(t *testing.T) {
	c := &Config{
		Database: DatabaseConfig{URI: "mongodb://127.0.0.1:27017/furry", Name: "furry", Pool: 10},
		Server: ServerConfig{
			NodeEnv:      "production",
			AllowOrigins: []string{"https://example.com"},
		},
		JWT: JWTConfig{Secret: "furry-drama-tracker-secret-key-2024"},
	}
	c.IsDev = false

	if err := c.validate(); err != nil {
		t.Fatalf("legacy production config should not block startup: %v", err)
	}
	if !c.JWT.WeakSecret {
		t.Fatal("expected weak secret to be flagged")
	}
	if !c.Server.InsecureOrigins {
		t.Fatal("expected placeholder origin to be flagged")
	}
}
