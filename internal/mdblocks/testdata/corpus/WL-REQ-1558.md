
The lifecycle hooks of a sandbox are installed at image build time. The agent is already running when the checkout appears, so hooks written then miss that session's `SessionStart`. This build-time rule covers hooks only.

