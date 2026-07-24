package cmd

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

func newMigrateCmd() *cobra.Command {
	var dsn, migrationsPath string
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply database migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dsn == "" {
				return errors.New("no DSN: set --dsn or LODE_DSN")
			}
			s, err := store.Open(dsn)
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.Migrate(migrationsPath); err != nil {
				return err
			}
			cmd.Println("migrations applied")
			return nil
		},
	}
	cmd.Flags().StringVar(&dsn, "dsn", os.Getenv("LODE_DSN"), "Postgres DSN (postgres://...); defaults to $LODE_DSN")
	cmd.Flags().StringVar(&migrationsPath, "migrations-path", "", "path to the directory containing *.up.sql/*.down.sql migration files")
	cmd.MarkFlagRequired("migrations-path")
	return cmd
}

func init() {
	rootCmd.AddCommand(newMigrateCmd())
}
