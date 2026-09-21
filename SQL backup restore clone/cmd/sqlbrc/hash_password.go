package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/DevOps-spb-ru/DevOps-Engineer-Tools/sql-backup-restore-clone/internal/passwd"
)

// newHashPasswordCommand создаёт bcrypt-хэш пароля для auth.users.
func newHashPasswordCommand(a *appState) *cobra.Command {
	var login string
	cmd := &cobra.Command{
		Use:   "hash-password",
		Short: "Создать bcrypt-хэш пароля для auth.users",
		Long: `hash-password читает пароль из stdin (первую строку) и печатает готовый фрагмент
конфига auth.users:

    printf '%s' 'пароль' | sqlbrc hash-password --login admin

Пароль не принимается флагом командной строки: аргументы видны в списке
процессов и остаются в истории shell. При вводе с терминала пароль отображается
эхом — если это неудобно, передайте его через stdin.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if strings.TrimSpace(login) == "" {
				return errors.New("не задан логин: укажите --login <имя>")
			}
			password, err := readPassword(a.stdin)
			if err != nil {
				return err
			}
			hash, err := passwd.Hash(password)
			if err != nil {
				return fmt.Errorf("%w (пароль должен быть не короче %d символов)",
					err, passwd.MinPasswordLength)
			}
			_, err = fmt.Fprintf(a.stdout, "auth:\n  users:\n    - login: %s\n      password_bcrypt: %q\n",
				login, hash)
			return err
		},
	}
	cmd.Flags().StringVar(&login, "login", "", "имя пользователя веб-интерфейса (обязательно)")
	return cmd
}

// readPassword читает пароль из stdin: берётся первая строка, поэтому пароль
// можно передать и через pipe, и ввести вручную.
func readPassword(stdin io.Reader) (string, error) {
	reader := bufio.NewReader(stdin)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("не удалось прочитать пароль из stdin: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
