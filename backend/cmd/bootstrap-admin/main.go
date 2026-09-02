package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/database"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres"
	"golang.org/x/term"
)

func main() {
	username := flag.String("username", "", "唯一管理员用户名")
	displayName := flag.String("display-name", "", "管理员显示名称")
	flag.Parse()
	if strings.TrimSpace(*username) == "" || strings.TrimSpace(*displayName) == "" {
		log.Fatal("username and display-name are required")
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	password, err := readConfirmedPassword()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	service, err := identity.NewService(postgres.New(pool), 24*time.Hour)
	if err != nil {
		log.Fatal(err)
	}
	account, err := service.CreateBootstrapAdmin(ctx, *username, *displayName, password)
	if err != nil {
		if errors.Is(err, identity.ErrConflict) {
			log.Fatal("bootstrap refused: an administrator already exists")
		}
		log.Fatal(err)
	}
	log.Printf("administrator %q created; first login must change the password", account.Username)
}

func readConfirmedPassword() (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("open controlling terminal: %w", err)
	}
	defer tty.Close()
	read := func(prompt string) (string, error) {
		if _, err := fmt.Fprint(tty, prompt); err != nil {
			return "", err
		}
		value, err := term.ReadPassword(int(tty.Fd()))
		_, _ = fmt.Fprintln(tty)
		return string(value), err
	}
	password, err := read("初始管理员密码（至少 12 位）：")
	if err != nil {
		return "", err
	}
	if err := identity.ValidatePassword(password); err != nil {
		return "", err
	}
	confirmation, err := read("再次输入密码：")
	if err != nil {
		return "", err
	}
	if password != confirmation {
		return "", errors.New("两次密码输入不一致")
	}
	return password, nil
}
