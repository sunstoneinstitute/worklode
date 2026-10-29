
A placeholder in a catalog template (WL-SPEC-74#sec-10.2a) is `{{ PLACEHOLDER }}` (inner whitespace optional). Rendering is verbatim single-pass byte substitution with no escaping, conditionals or nesting; a credential value containing `{{ … }}` is substituted literally. Every placeholder must have a `cred.` key and every `cred.` key must be used. Any other `{{` is an error. There is no escape for a literal `{{`.

