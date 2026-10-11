package configstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/hyaeve/manco/internal/store"
)

func TestPersistAndRestoreAccountsUsesPasswordHash(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "manco.db")
	configDir := filepath.Join(root, "config")
	repository, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("plain-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateUser(ctx, "admin", string(hash)); err != nil {
		t.Fatal(err)
	}
	files, err := New(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := files.PersistAll(ctx, repository); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(configDir, accountsFile))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "plain-password") || !strings.Contains(text, string(hash)) {
		t.Fatalf("accounts file does not contain the expected password hash: %s", text)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := files.Restore(ctx, reopened); err != nil {
		t.Fatal(err)
	}
	user, err := reopened.UserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if user.PasswordHash != string(hash) {
		t.Fatalf("restored password hash = %q, want %q", user.PasswordHash, hash)
	}
}
