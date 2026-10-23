package migrate

import (
	"context"
	"fmt"
	"os"

	"github.com/scylladb/gocqlx/v3/migrate"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/ssenerg/gochat/cmd/version"
	"github.com/ssenerg/gochat/internal/config"
	"github.com/ssenerg/gochat/internal/repos/scylla"
	"github.com/ssenerg/gochat/internal/utils"
	"github.com/ssenerg/gochat/internal/utils/logging"
)

func requirements(args []string) (*zap.Logger, *Config, error) {
	version.Header()
	cfg := Config{}
	if err := config.LoadConfig(
		&cfg,
		utils.EnvOr("CONFIG_ENV_PREFIX", configEnvPrefix),
		utils.EnvOr("CONFIG_PATH", configPath),
		utils.EnvOr("CONFIG_NAME", configName),
		defaultConfig,
	); err != nil {
		return nil, nil, fmt.Errorf("failed to load config: %w", err)
	}
	logger, err := logging.New(cfg.Logging)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create logger: %w", err)
	}
	logger = logger.Named("scylla")
	if err := utils.ValidateDir(args[0]); err != nil {
		logger.Error("invalid migration directory", zap.Error(err))
		return nil, nil, fmt.Errorf("invalid migration directory: %w", err)
	}
	return logger, &cfg, nil
}

func scyllaUp(cmd *cobra.Command, args []string) error {
	logger, cfg, err := requirements(args)
	if err != nil {
		return err
	}
	defer func() { _ = logger.Sync() }()

	if err := scylla.EnsureKeyspace(logger, cfg.Database.Scylla); err != nil {
		logger.Error("failed to prepare keyspace", zap.Error(err))
		return err
	}
	session, err := scylla.Open(logger, cfg.Database.Scylla)
	if err != nil {
		return err
	}
	defer session.Close()

	logger.Info("applying migrations", zap.String("migration-dir", args[0]))
	migrate.DefaultAwaitSchemaAgreement = migrate.AwaitSchemaAgreementBeforeEachFile
	if err := migrate.FromFS(cmd.Context(), session, os.DirFS(args[0])); err != nil {
		logger.Error("failed to apply migrations", zap.Error(err))
		return fmt.Errorf("failed to apply migrations: %w", err)
	}
	logger.Info("migrations applied successfully")
	return nil
}

func scyllaStatus(cmd *cobra.Command, args []string) error {
	logger, cfg, err := requirements(args)
	if err != nil {
		return err
	}
	defer func() { _ = logger.Sync() }()

	session, err := scylla.Open(logger, cfg.Database.Scylla)
	if err != nil {
		return err
	}
	defer session.Close()

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	applied, err := migrate.List(ctx, session)
	if err != nil {
		return fmt.Errorf("failed to list migrations: %w", err)
	}
	pending, err := migrate.Pending(ctx, session, os.DirFS(args[0]))
	if err != nil {
		return fmt.Errorf("failed to list pending migrations: %w", err)
	}
	for _, m := range applied {
		fmt.Printf("applied  %s  %s\n", m.EndTime.Format("2006-01-02 15:04:05"), m.Name)
	}
	for _, m := range pending {
		fmt.Printf("pending  %s\n", m.Name)
	}
	return nil
}
