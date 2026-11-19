package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"knowledge-base/internal/kb"
)

func run() error {
	// The extraction child runs without a database or any server secrets.
	if len(os.Args) > 1 && os.Args[1] == "extract-text" {
		return kb.RunExtractTextCommand(os.Args[2:], os.Stdout)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	connectCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	store, err := kb.Open(connectCtx, dsn)
	stop()
	if err != nil {
		return err
	}
	defer store.DB.Close()
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	switch command {
	case "migrate":
		return store.Migrate(ctx)
	case "bootstrap":
		return store.Bootstrap(ctx, os.Getenv("BOOTSTRAP_EMAIL"), os.Getenv("BOOTSTRAP_NAME"), os.Getenv("BOOTSTRAP_PASSWORD"))
	case "serve":
		style := os.Getenv("BUCKET_FORCE_PATH_STYLE")
		if style != "" && style != "false" && style != "true" {
			return errors.New("BUCKET_FORCE_PATH_STYLE must be true or false")
		}
		store.Objects, err = kb.NewRailwayBucket(kb.BucketConfig{Endpoint: os.Getenv("BUCKET_ENDPOINT"), Bucket: os.Getenv("BUCKET_NAME"), Region: os.Getenv("BUCKET_REGION"), AccessKeyID: os.Getenv("BUCKET_ACCESS_KEY_ID"), SecretAccessKey: os.Getenv("BUCKET_SECRET_ACCESS_KEY"), ForcePathStyle: style == "true"})
		if err != nil {
			return err
		}
		if store.AttachmentQuotaBytes, err = kb.ParseAttachmentQuota(os.Getenv("ATTACHMENT_PROJECT_QUOTA_MB")); err != nil {
			return err
		}
		allowExec := os.Getenv("ATTACHMENT_ALLOW_EXECUTABLES")
		if allowExec != "" && allowExec != "true" && allowExec != "false" {
			return errors.New("ATTACHMENT_ALLOW_EXECUTABLES must be true or false")
		}
		store.AllowExecutables = allowExec == "true"
		if address := os.Getenv("CLAMAV_ADDRESS"); address != "" {
			if _, _, err := net.SplitHostPort(address); err != nil {
				return errors.New("CLAMAV_ADDRESS must be host:port")
			}
			store.Scanner = kb.ClamAV{Address: address}
		}
		textExtraction := os.Getenv("ATTACHMENT_TEXT_EXTRACTION")
		if textExtraction != "" && textExtraction != "true" && textExtraction != "false" {
			return errors.New("ATTACHMENT_TEXT_EXTRACTION must be true or false")
		}
		if textExtraction != "false" {
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			store.Extractor = kb.ProcessExtractor{Executable: executable}
		}
		if store.MFAKey, err = kb.ParseMFAKey(os.Getenv("MFA_ENCRYPTION_KEY")); err != nil {
			return err
		}
		if store.GitHub, err = kb.NewGitHubClient(os.Getenv("GITHUB_API_URL"), os.Getenv("GITHUB_TOKEN")); err != nil {
			return err
		}
		if store.Deployment, err = kb.DetectDeploymentTarget(os.Getenv("SERVER_REPOSITORY"), os.Getenv("RAILWAY_GIT_REPO_OWNER"), os.Getenv("RAILWAY_GIT_REPO_NAME")); err != nil {
			return err
		}
		if deploymentChecks := os.Getenv("DEPLOYMENT_CHECKS"); deploymentChecks != "" && deploymentChecks != "true" && deploymentChecks != "false" {
			return errors.New("DEPLOYMENT_CHECKS must be true or false")
		} else if deploymentChecks != "false" {
			workerCtx, stopWorker := context.WithCancel(ctx)
			workerDone := make(chan struct{})
			go func() { defer close(workerDone); store.RunDeploymentChecks(workerCtx) }()
			defer func() { stopWorker(); <-workerDone }()
		}
		if store.HNSWReview, err = kb.ParseHNSWThresholds(os.Getenv("HNSW_REVIEW_VECTOR_P95_MS"), os.Getenv("HNSW_REVIEW_SCANNED_CHUNKS")); err != nil {
			return err
		}
		if store.Search, err = kb.ParseSearchPolicy(os.Getenv("ISSUE_SCORE_FLOOR")); err != nil {
			return err
		}
		if store.Retention, err = kb.ParseRetention(os.Getenv("AUDIT_RETENTION_DAYS"), os.Getenv("REVISION_RETENTION_DAYS"), os.Getenv("REVISION_KEEP_LATEST")); err != nil {
			return err
		}
		{
			workerCtx, stopWorker := context.WithCancel(ctx)
			workerDone := make(chan struct{})
			go func() { defer close(workerDone); store.RunRetention(workerCtx) }()
			defer func() { stopWorker(); <-workerDone }()
		}
		if store.Objects != nil {
			workerCtx, stopWorker := context.WithCancel(ctx)
			workerDone := make(chan struct{})
			go func() { defer close(workerDone); store.RunAttachmentCleanup(workerCtx) }()
			defer func() { stopWorker(); <-workerDone }()
		}
		notifier, err := notifierConfig()
		if err != nil {
			return err
		}
		embeddingEnabled := os.Getenv("EMBEDDING_ENABLED")
		if embeddingEnabled != "" && embeddingEnabled != "true" && embeddingEnabled != "false" {
			return errors.New("EMBEDDING_ENABLED must be true or false")
		}
		if embeddingEnabled != "false" {
			baseURL, model := os.Getenv("OLLAMA_BASE_URL"), os.Getenv("EMBEDDING_MODEL")
			if baseURL == "" {
				baseURL = "http://localhost:11434"
			}
			if model == "" {
				model = "bge-m3"
			}
			threads, err := kb.ParseEmbeddingThreads(os.Getenv("EMBEDDING_NUM_THREADS"))
			if err != nil {
				return err
			}
			embedder, err := kb.NewOllamaEmbedder(baseURL, model)
			if err != nil {
				return err
			}
			embedder.NumThreads = threads
			store.Embedder = embedder
			workerCtx, stopWorker := context.WithCancel(ctx)
			workerDone := make(chan struct{})
			go func() { defer close(workerDone); store.RunEmbeddingWorker(workerCtx) }()
			defer func() { stopWorker(); <-workerDone }()
		}
		port, config, err := serveConfig()
		if err != nil {
			return err
		}
		if needed, err := store.NeedsSetup(ctx); err == nil && needed {
			slog.Warn("no administrator exists: open the public URL to create the first one", "public_url", config.PublicURL)
		}
		app, err := kb.NewApp(store, config)
		if err != nil {
			return err
		}
		// NewApp sets store.PublicURL, which notification links use, so the
		// notification worker starts only after it.
		if notifier != nil {
			store.Notifier = notifier
			workerCtx, stopWorker := context.WithCancel(ctx)
			workerDone := make(chan struct{})
			go func() { defer close(workerDone); store.RunNotificationWorker(workerCtx) }()
			defer func() { stopWorker(); <-workerDone }()
		}
		server := &http.Server{Addr: ":" + port, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 120 * time.Second, WriteTimeout: 130 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
		go func() {
			ticker := time.NewTicker(time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					cleanCtx, done := context.WithTimeout(ctx, 15*time.Second)
					if err := store.Cleanup(cleanCtx); err != nil {
						slog.Error("expired credential cleanup failed")
					}
					done()
				}
			}
		}()
		errCh := make(chan error, 1)
		go func() {
			slog.Info("knowledge server listening", "port", port, "public_url", config.PublicURL, "environment", os.Getenv("APP_ENV"))
			errCh <- server.ListenAndServe()
		}()
		select {
		case <-ctx.Done():
			shutdownCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
			defer done()
			return server.Shutdown(shutdownCtx)
		case err := <-errCh:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		}
	default:
		return errors.New("usage: knowledge-base [serve|migrate|bootstrap]")
	}
}
func main() {
	if err := run(); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}
