package internal

var (
	appName       = "gochat"
	appNamePretty = "GoChat"
	version       = "v0.0.0-dev"
)

func AppName() string {
	return appName
}

func AppNamePretty() string {
	return appNamePretty
}

func Version() string {
	return version
}
