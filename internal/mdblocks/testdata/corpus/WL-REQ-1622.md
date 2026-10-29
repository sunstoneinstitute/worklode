
The model's answer to the frame of WL-REQ-157 is one of:

| Automation form | Accepted answers |
|---|---|
| `then.to` or single-target `choose` | `{"match": true}` or `{"match": false}` |
| Multi-target `choose` | `{"choice": <zero-based index into the automation's choose array>}` or `{"choice": null}` |

Anything else (free text, out-of-range index, a state name, extra keys, JSON in prose) is `unparseable` and the automation does not match. Model output never names a state.

