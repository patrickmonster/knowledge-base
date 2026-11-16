package main

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"

	"knowledge-base/internal/kb"
)

// notifierConfig enables issue notification delivery unless
// NOTIFICATIONS_ENABLED=false. Email channels additionally need SMTP_HOST and
// SMTP_FROM; SMTP_USERNAME/SMTP_PASSWORD are optional (PLAIN auth over TLS).
func notifierConfig() (*kb.Notifier, error) {
	enabled := os.Getenv("NOTIFICATIONS_ENABLED")
	if enabled != "" && enabled != "true" && enabled != "false" {
		return nil, errors.New("NOTIFICATIONS_ENABLED must be true or false")
	}
	if enabled == "false" {
		return nil, nil
	}
	digest, err := recheckDigestConfig()
	if err != nil {
		return nil, err
	}
	host := os.Getenv("SMTP_HOST")
	if host == "" {
		n := kb.NewNotifier(nil)
		n.Digest = digest
		return n, nil
	}
	port := 587
	if raw := os.Getenv("SMTP_PORT"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 65535 {
			return nil, errors.New("SMTP_PORT must be between 1 and 65535")
		}
		port = n
	}
	from := os.Getenv("SMTP_FROM")
	if from == "" || strings.ContainsAny(from, "\r\n") {
		return nil, errors.New("SMTP_FROM is required when SMTP_HOST is set")
	}
	if (os.Getenv("SMTP_USERNAME") == "") != (os.Getenv("SMTP_PASSWORD") == "") {
		return nil, errors.New("set SMTP_USERNAME and SMTP_PASSWORD together")
	}
	n := kb.NewNotifier(&kb.SMTPConfig{Host: host, Port: port, Username: os.Getenv("SMTP_USERNAME"), Password: os.Getenv("SMTP_PASSWORD"), From: from})
	n.Digest = digest
	return n, nil
}

// recheckDigestConfig reads RECHECK_DIGEST_SCHEDULE (off, daily or weekly;
// default weekly) and RECHECK_DIGEST_HOUR (0-23, Asia/Seoul; default 9). A
// digest is only sent to projects with a channel subscribed to recheck_digest.
func recheckDigestConfig() (kb.RecheckDigestSchedule, error) {
	c := kb.RecheckDigestSchedule{Frequency: "weekly", Hour: 9}
	if raw := os.Getenv("RECHECK_DIGEST_SCHEDULE"); raw != "" {
		if raw != "off" && raw != "daily" && raw != "weekly" {
			return c, errors.New("RECHECK_DIGEST_SCHEDULE must be off, daily, or weekly")
		}
		c.Frequency = raw
	}
	if c.Frequency == "off" {
		c.Frequency = ""
	}
	if raw := os.Getenv("RECHECK_DIGEST_HOUR"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 23 {
			return c, errors.New("RECHECK_DIGEST_HOUR must be between 0 and 23")
		}
		c.Hour = n
	}
	return c, nil
}

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
