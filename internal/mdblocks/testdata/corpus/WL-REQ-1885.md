
A crit comment carries no intent, so `lode-server` classifies the intent of each thread a crit round opens (WL-REQ-1776) as one of the WL-REQ-1318 intents with TypeSafe Jev. `lode review import` sends the round's threads with no intent; every other client states the intent itself, and the server never reclassifies a stated one.

The classifier sends one Jev Choice question per new thread, batched into one `POST /v1/systemone` request per round. The question's state holds the comment's body, its quote, its replies, and the review subject's kind and title. Its criteria are `note`, `change_request` and `question`, each described by what it means and whom it obliges in WL-REQ-1318. The thread takes the chosen intent and records `jev` as its intent classifier with the answer's confidence.

When `LODE_TYPESAFE_API_KEY` is unset or the call fails, every thread of the round takes `change_request` and records `default` as its intent classifier. The round is never refused or delayed for a classification. `change_request` is the fallback because it keeps the reviewer's comment binding on the author; a request misread as a `note` would oblige nobody.

The call never runs inside a store write transaction: the server classifies first, then writes the round. The API key is the server setting `LODE_TYPESAFE_API_KEY` and never leaves the server, as for WL-REQ-1857.

Checks: a handler test with a stubbed TypeSafe server proves a crit round's threads take the chosen intents with `jev` recorded, a stated intent from another client is kept, and an unset key or a failing call gives `change_request` with `default` and the round still lands.
