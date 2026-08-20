package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func newActorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "actor",
		Short: "Manage actors (humans, agents, service accounts)",
	}
	cmd.AddCommand(newActorAddCmd())
	return cmd
}

func newActorAddCmd() *cobra.Command {
	var kind, name string
	var admin bool
	cmd := &cobra.Command{
		Use:   "add <id>",
		Short: "Create an actor",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			a, raw, err := c.CreateActor(cmd.Context(), model.CreateActorInput{
				ID: args[0], Kind: kind, DisplayName: name, Admin: admin,
			})
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created actor %s (%s)\n", a.ID, a.Kind)
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "actor kind: human, agent, or service (required)")
	cmd.Flags().StringVar(&name, "name", "", "display name")
	cmd.Flags().BoolVar(&admin, "admin", false, "grant admin rights (manage projects, actors, and tokens)")
	cmd.MarkFlagRequired("kind")
	return cmd
}

func newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage bearer tokens",
	}
	cmd.AddCommand(newTokenCreateCmd(), newTokenRevokeCmd())
	return cmd
}

func newTokenCreateCmd() *cobra.Command {
	var actor, description, expiresAt string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Mint a bearer token for an actor (printed once — save it now)",
		RunE: func(cmd *cobra.Command, args []string) error {
			var exp *time.Time
			if expiresAt != "" {
				t, err := time.Parse(time.RFC3339, expiresAt)
				if err != nil {
					return fmt.Errorf("invalid --expires-at %q: must be RFC3339 (e.g. 2026-12-31T00:00:00Z): %w", expiresAt, err)
				}
				exp = &t
			}
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			resp, raw, err := c.CreateToken(cmd.Context(), actor, description, exp)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), resp.Token)
			return nil
		},
	}
	cmd.Flags().StringVar(&actor, "actor", "", "actor id to mint the token for (required)")
	cmd.Flags().StringVar(&description, "description", "", "human-readable note about this token's purpose")
	cmd.Flags().StringVar(&expiresAt, "expires-at", "", "RFC3339 expiry (default: never expires)")
	cmd.MarkFlagRequired("actor")
	return cmd
}

func newTokenRevokeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke <token>",
		Short: "Revoke a bearer token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			raw, err := c.RevokeToken(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "token revoked")
			return nil
		},
	}
	return cmd
}

func newBlobCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "blob",
		Short: "Manage content-addressed blobs",
	}
	cmd.AddCommand(newBlobGCCmd())
	return cmd
}

// newBlobGCCmd runs both GC sweeps from spec 021 §11. --dry-run defaults to
// true — running the real sweep should be a deliberate act, not the default
// of a command an operator ran to see what would happen.
func newBlobGCCmd() *cobra.Command {
	var apply bool
	var graceHours int
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Collect unreferenced blobs and orphan objects",
		Long: "Reports by default. Pass --apply to delete. Grace period keeps both\n" +
			"sweeps clear of uploads in flight.",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			res, raw, err := c.BlobGC(cmd.Context(), !apply, &graceHours)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				printRaw(cmd, raw)
				return nil
			}
			w := cmd.OutOrStdout()
			verb := "would delete"
			if apply {
				verb = "deleted"
			}
			fmt.Fprintf(w, "%d unreferenced blob(s), %d orphan object(s); %s %d\n",
				len(res.Unreferenced), len(res.OrphanObjects), verb, res.Deleted)
			for _, e := range res.Errors {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", e)
			}
			if len(res.Errors) > 0 {
				return fmt.Errorf("%d gc error(s)", len(res.Errors))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "actually delete (default: report only)")
	cmd.Flags().IntVar(&graceHours, "grace-hours", 24, "ignore blobs and objects newer than this")
	return cmd
}

func init() {
	rootCmd.AddCommand(newActorCmd())
	rootCmd.AddCommand(newTokenCmd())
	rootCmd.AddCommand(newBlobCmd())
}
