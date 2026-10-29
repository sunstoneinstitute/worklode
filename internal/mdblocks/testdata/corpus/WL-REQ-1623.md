
Transport error, timeout, HTTP failure, `unparseable`, exhausted budget, or no provider configured: the automation does not match and evaluation falls through. A deployment with no LLM configured runs structured automations at full fidelity and treats every prompt automation as never matching (`unconfigured`).

Three independent layers stand between adversarial model output and an illegal transition: write-time validation (WL-SPEC-76#sec-10.2a: the config cannot store a forbidden edge, and the engine has no write path to config), answer-space bounding (WL-SPEC-76#sec-10.4a: output selects among pre-validated targets or declines), and the guard (WL-REQ-146: every firing passes the per-task legality check). The worst a total compromise of the model achieves is a premature but legal transition, evented, attributed, and reversible through the core reopen edge.

