
A `/api/v1` write that refuses a body because of specific fields answers 422
with `model.ErrorResponse`, whose new optional `fields` member maps each
field's wire name to a message:
`{ "error": "invalid idea", "fields": { "title": "Give your idea a title." } }`.
`error` stays as it is, so the CLI keeps reading one message.

The form shows each message under its field, and one for a field it does not
show at the top. Any other error shows a toast and keeps what was typed.
