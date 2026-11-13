package main

import "testing"

func TestServeConfig(t *testing.T) {
	tests := []struct {
		name   string
		env    map[string]string
		origin string
		fail   bool
	}{
		{"local defaults", nil, "http://localhost:8080", false},
		{"Railway generated domain", map[string]string{"APP_ENV": "development", "RAILWAY_PUBLIC_DOMAIN": "kb-dev.up.railway.app"}, "https://kb-dev.up.railway.app", false},
		{"custom domain", map[string]string{"APP_ENV": "production", "PUBLIC_URL": "https://knowledge.example.com/", "RAILWAY_PUBLIC_DOMAIN": "kb.up.railway.app"}, "https://knowledge.example.com", false},
		{"production missing domain", map[string]string{"APP_ENV": "production"}, "", true},
		{"Railway missing domain", map[string]string{"RAILWAY_ENVIRONMENT_ID": "environment-id"}, "", true},
		{"production local origin", map[string]string{"APP_ENV": "production", "PUBLIC_URL": "http://localhost:8080"}, "", true},
		{"invalid port", map[string]string{"PORT": "65536"}, "", true},
		{"invalid environment", map[string]string{"APP_ENV": "prodution"}, "", true},
		{"invalid proxy setting", map[string]string{"TRUST_PROXY": "ture"}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{"PORT", "APP_ENV", "PUBLIC_URL", "RAILWAY_PUBLIC_DOMAIN", "RAILWAY_ENVIRONMENT_ID", "TRUST_PROXY"} {
				t.Setenv(key, "")
			}
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			_, config, err := serveConfig()
			if (err != nil) != tt.fail {
				t.Fatalf("error = %v, want failure = %v", err, tt.fail)
			}
			if !tt.fail && config.PublicURL != tt.origin {
				t.Fatalf("origin = %q, want %q", config.PublicURL, tt.origin)
			}
		})
	}
}
