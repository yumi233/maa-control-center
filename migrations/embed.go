package migrations

import _ "embed"

//go:embed 001_initial.sql
var Initial string

//go:embed 002_agent_mode.sql
var AgentMode string

//go:embed 003_multi_user.sql
var MultiUser string

//go:embed 004_execution.sql
var Execution string
