package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newVersionCommand печатает версию, коммит и дату сборки: значения подставляет
// сборка через -ldflags, поэтому команда работает и из исходников.
func newVersionCommand(a *appState) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Показать версию, коммит и дату сборки",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(a.stdout, "sqlbrc %s\n", versionString())
			return err
		},
	}
}
