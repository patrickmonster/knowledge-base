package migrations

import _ "embed"

//go:embed 001_initial.sql
var Initial string

//go:embed 002_auth.sql
var Auth string

//go:embed 003_embeddings.sql
var Embeddings string
