
The webhook handlers of WL-REQ-114 need these GitHub App permissions: Actions: read, Deployments: read, Contents: read, Packages: read; and webhook subscriptions `push`, `deployment_status`, `registry_package` beside the existing ones. Flux notification-controller in every cluster gets a Provider/Alert pointing at `/hooks/flux`.

