
An authorization code exists only after the signed-in user pressed Allow on a consent page that names who is signing in and where the token goes.

Why: Keycloak signs a logged-in user in without asking, and the client is public. Without consent, a crafted authorize link would hand that user's token to whoever chose the redirect.

