package cmd

import (
	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

func newMigrateCmd() *cobra.Command {
	var dbPath, migrationsPath string
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply database migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(dbPath)
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
	cmd.Flags().StringVar(&dbPath, "db", "", "path to the SQLite database file")
	cmd.MarkFlagRequired("db")
	cmd.Flags().StringVar(&migrationsPath, "migrations-path", "", "path to the directory containing *.up.sql/*.down.sql migration files")
	cmd.MarkFlagRequired("migrations-path")
	return cmd
}

func init() {
	rootCmd.AddCommand(newMigrateCmd())
}
