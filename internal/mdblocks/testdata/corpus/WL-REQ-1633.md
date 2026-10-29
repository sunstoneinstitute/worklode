
A rule's canonical page is `/projects/<proj>/rule/<n>`, with `/<ver>` for one version. It is the durable deep link to the section the rule fills in any spec that arranges it, since a `<doc>#sec-N` link follows the arrangement (WL-REQ-165). `/rules/<ref>` redirects there from any infix.

Stored text cites a rule by its bare ref, and rendering links it. The autolinker targets the `/rules/<ref>` redirect, except on a spec's page, where a ref to a rule that spec arranges links to the rule's section on the same page (`#sec-N`).
