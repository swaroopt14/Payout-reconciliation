// Command reencrypt-secrets seals legacy plaintext connectors.secret values
// with CLEARLINE_SECRETS_KEY (enc:v1:). Idempotent: safe to rerun; rows that
// are already encrypted are skipped. It prints only a count, never a value.
//
// Operator step (D32): run once per environment after setting
// CLEARLINE_SECRETS_KEY and before relying on the new edge build, because
// edge rejects legacy plaintext secrets on read.
package main

import (
	"context"
	"fmt"
	"os"

	"zord-edge/config"
	"zord-edge/db"
	"zord-edge/logger"
	"zord-edge/services"

	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()
	logger.Init("zord-edge-reencrypt-secrets")
	config.InitDB()
	if db.DB == nil {
		fmt.Fprintln(os.Stderr, "database handle is nil")
		os.Exit(1)
	}
	n, err := services.ReEncryptConnectorSecrets(context.Background(), services.SQLConnectorSecretStore{DB: db.DB})
	if err != nil {
		fmt.Fprintf(os.Stderr, "re-encrypt failed after %d rows: %v\n", n, err)
		os.Exit(1)
	}
	fmt.Printf("re-encrypted %d connector secret(s)\n", n)
}
