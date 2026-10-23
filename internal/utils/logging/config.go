package logging

import (
	"errors"
	"fmt"

	"github.com/ssenerg/gochat/internal/config"

	"go.uber.org/zap/zapcore"
)

type Config struct {
	Console *ConsoleConfig `mapstructure:"console"`
	File    *FileConfig    `mapstructure:"file"`
	Version bool           `mapstructure:"version"` // whether to enable version logging
}

var _ config.Config = (*Config)(nil)

func (c *Config) Validate() error {
	if c.Console == nil {
		return errors.New("console is required")
	}
	if err := c.Console.validate(); err != nil {
		return fmt.Errorf("console.%w", err)
	}
	if c.File == nil {
		return errors.New("file is required")
	}
	if err := c.File.validate(); err != nil {
		return fmt.Errorf("file.%w", err)
	}
	return nil
}

type ConsoleConfig struct {
	Enabled bool          `mapstructure:"enabled"` // whether to enable console logging
	Level   LoggingLevel  `mapstructure:"level"`   // the level of the logs
	Format  LoggingFormat `mapstructure:"format"`  // the format of the logs
}

func (c *ConsoleConfig) validate() error {
	if !c.Enabled {
		return nil
	}
	if err := c.Level.validate(); err != nil {
		return err
	}
	if err := c.Format.validate(); err != nil {
		return err
	}
	return nil
}

type FileConfig struct {
	Enabled    bool          `mapstructure:"enabled"`     // whether to enable file logging
	Name       string        `mapstructure:"name"`        // the name of the log file
	Level      LoggingLevel  `mapstructure:"level"`       // the level of the logs
	Format     LoggingFormat `mapstructure:"format"`      // the format of the logs
	Folder     string        `mapstructure:"folder"`      // the folder to store the logs
	MaxSize    int           `mapstructure:"max_size"`    // in megabytes
	MaxAge     int           `mapstructure:"max_age"`     // in days
	MaxBackups int           `mapstructure:"max_backups"` // number of backups
	Compress   bool          `mapstructure:"compress"`    // whether to compress the backups in gzip format
}

func (c *FileConfig) validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Name == "" {
		return errors.New("name is required")
	}
	if err := c.Level.validate(); err != nil {
		return err
	}
	if err := c.Format.validate(); err != nil {
		return err
	}
	if c.Folder == "" {
		return errors.New("folder is required")
	}
	if c.MaxSize <= 0 {
		return errors.New("max_size must be greater than 0")
	}
	if c.MaxAge <= 0 {
		return errors.New("max_age must be greater than 0")
	}
	if c.MaxBackups <= 0 {
		return errors.New("max_backups must be greater than 0")
	}
	return nil
}

type LoggingLevel string

const (
	LoggingLevelDebug LoggingLevel = "debug"
	LoggingLevelInfo  LoggingLevel = "info"
	LoggingLevelWarn  LoggingLevel = "warn"
	LoggingLevelError LoggingLevel = "error"
	LoggingLevelFatal LoggingLevel = "fatal"
)

func (l LoggingLevel) validate() error {
	switch l {
	case LoggingLevelDebug, LoggingLevelInfo, LoggingLevelWarn, LoggingLevelError, LoggingLevelFatal:
		return nil
	default:
		return fmt.Errorf("level: %s", l)
	}
}

func (l LoggingLevel) zapLevel() zapcore.Level {
	switch l {
	case LoggingLevelDebug:
		return zapcore.DebugLevel
	case LoggingLevelInfo:
		return zapcore.InfoLevel
	case LoggingLevelWarn:
		return zapcore.WarnLevel
	case LoggingLevelError:
		return zapcore.ErrorLevel
	case LoggingLevelFatal:
		return zapcore.FatalLevel
	default:
		return zapcore.InfoLevel
	}
}

type LoggingFormat string

const (
	LoggingFormatJSON LoggingFormat = "json"
	LoggingFormatText LoggingFormat = "text"
)

func (f LoggingFormat) validate() error {
	switch f {
	case LoggingFormatJSON, LoggingFormatText:
		return nil
	default:
		return fmt.Errorf("format: %s", f)
	}
}
