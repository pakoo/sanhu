package config

import (
	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

var (
	_ = cron.New
	_ yaml.Node
)
