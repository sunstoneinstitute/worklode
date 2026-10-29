
One App per environment, because an App has one webhook URL and one callback set. The production App is named `WorklodeBot`, the name GitHub shows in the attribution.

| Setting | Value |
|---|---|
| User authorization | Enabled, expiring user tokens (8 h access, about 6-month refresh) |
| Callback URL | `{LODE_PUBLIC_URL}/auth/github/callback` |
| Webhook URL | `{LODE_PUBLIC_URL}/hooks/github`, HMAC via `LODE_GITHUB_WEBHOOK_SECRET` |
| Permissions | Write: Contents, Pull requests, Issues, Commit statuses. Read: Actions, Checks, Deployments, Environments, Merge queues, Members, Metadata, Packages, Repository advisories, Vulnerability alerts. Contents and Pull requests write let agent sessions push and open pull requests as their human (WL-REQ-1292). |
| Installation | `sunstoneinstitute` org, selected repositories only. The provisioning and admin-cluster repos are excluded. |

