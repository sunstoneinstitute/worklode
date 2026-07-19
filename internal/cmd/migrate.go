package cmd

import (
	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/work-tracker/internal/store"
)

func newMigrateCmd() *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply database migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer s.Close()
			cmd.Println("migrations applied")
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "path to the SQLite database file")
	cmd.MarkFlagRequired("db")
	return cmd
}

func init() {
	rootCmd.AddCommand(newMigrateCmd())
}
