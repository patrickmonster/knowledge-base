package migrations

import _ "embed"

//go:embed 001_initial.sql
var Initial string

//go:embed 002_auth.sql
var Auth string

//go:embed 003_embeddings.sql
var Embeddings string

//go:embed 004_permanent_keys.sql
var PermanentKeys string

//go:embed 005_task_workflow.sql
var TaskWorkflow string
