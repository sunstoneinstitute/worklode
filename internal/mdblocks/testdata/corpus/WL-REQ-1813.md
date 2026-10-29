
The SPA keeps an unsent copy of each form in `localStorage`, so a refresh, a
crash or a closed tab loses nothing. One hook, `useDraft`, gives every form
this behavior.

- The key is `worklode:v1:draft:<actor>:<form>` for a new record and
  `worklode:v1:draft:<actor>:<form>:<recordId>` for an edit. `<form>` is a
  name the form passes to the hook, and `<actor>` is the session's actor from
  `GET /api/v1/whoami`, so a second person in the same browser never sees
  another person's draft.
- The copy is written about 500 ms after typing stops. Nothing reaches the
  server until the person submits.
- A form that opens with a draft fills its fields from the draft, ignores
  values for fields it no longer has, and shows "Draft restored · Discard".
  Discard deletes the draft and restores the starting values.
- A draft is deleted after a successful submit and by Discard.
- Every `worklode:v1:draft:*` key is deleted when a `/api/v1` request answers
  401, and by logout once logout exists. Drafts can hold unpublished work, so
  they must not outlive the session that wrote them. If drafts exist, logout
  first asks: "You have unsaved drafts. Logging out deletes them."
- Every read is validated and wrapped in try/catch. Missing or bad data, a
  private window and blocked storage all fall back to the starting values.

Drafts are local state like the view preferences of WL-REQ-1772, but they
hold a person's content, which is why they are keyed by actor and cleared
with the session.
