package main

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"

	"knowledge-base/internal/kb"
)

func serveConfig() (string, kb.Config, error) {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", kb.Config{}, errors.New("PORT must be between 1 and 65535")
	}
	env := os.Getenv("APP_ENV")
	if env != "" && env != "local" && env != "development" && env != "production" {
		return "", kb.Config{}, errors.New("APP_ENV must be local, development, or production")
	}
	origin := strings.TrimSuffix(os.Getenv("PUBLIC_URL"), "/")
	if origin == "" && os.Getenv("RAILWAY_PUBLIC_DOMAIN") != "" {
		origin = "https://" + os.Getenv("RAILWAY_PUBLIC_DOMAIN")
	}
	hosted := env == "development" || env == "production" || os.Getenv("RAILWAY_ENVIRONMENT_ID") != ""
	if hosted {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" {
			return "", kb.Config{}, errors.New("hosted environments require an HTTPS PUBLIC_URL or a Railway public domain")
		}
	} else if origin == "" {
		origin = "http://localhost:" + port
	}
	trustProxy := os.Getenv("TRUST_PROXY")
	if trustProxy != "" && trustProxy != "true" && trustProxy != "false" {
		return "", kb.Config{}, errors.New("TRUST_PROXY must be true or false")
	}
	return port, kb.Config{PublicURL: origin, TrustProxy: trustProxy == "true"}, nil
}
