
The engine builds the whole request for a prompt automation. The author contributes only `prompt`, which arrives through the reviewed PUT and is trusted policy. The fixed frame contains: the prompt, the candidate target state names, task facts (id, title, kind, state, governing workflow), the triggering edge, the last 5 `state_log` entries, and the task body truncated to 2000 characters inside delimiters the frame marks as untrusted content not to be followed as instructions. No tools, no fetching, no repo contents, no other tasks. Temperature 0, small `max_tokens`, JSON object only.

