package main

import (
	"context"
	"fmt"
	"log"

	"zulip-collector/internal/config"
)

func main() {
	ctx := context.Background()

	db, err := config.OpenPostgres(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	var database string
	var username string
	var version string
	var schemaReady bool

	err = db.QueryRowContext(ctx, `
		SELECT
			current_database(),
			current_user,
			current_setting('server_version'),
			to_regclass('zulip.messages') IS NOT NULL
	`).Scan(
		&database,
		&username,
		&version,
		&schemaReady,
	)

	if err != nil {
		log.Fatal(err)
	}

	if database != "sop_db" {
		log.Fatalf("unexpected database: %s", database)
	}

	if !schemaReady {
		log.Fatal("zulip.messages table not found")
	}

	fmt.Println("PostgreSQL connected")
	fmt.Println("Database:", database)
	fmt.Println("User:", username)
	fmt.Println("Version:", version)
	fmt.Println("Zulip schema: OK")
}
