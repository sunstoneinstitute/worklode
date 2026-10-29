
- Whether `Spec: none config` should exist, or whether config-value changes
  should not touch guarded paths in the first place.
- Whether the `spec_gate` event (see "Designed, not built") should also feed
  the Progress page's next-act derivation (WL-SPEC-85 §2), so a section with
  many `fix` citations shows as one that needs rewriting.
- Which token the CI job uses once it resolves refs against the server. A
  per-repo read-only token is the smallest choice. The actor model in
  WL-SPEC-74 §1 decides.

