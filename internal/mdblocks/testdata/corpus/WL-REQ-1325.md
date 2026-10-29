
Nothing opens a change review on its own. A pull request opening, a task
reaching `in_review`, or CI passing opens no review and mints no review task.
A change review exists only when an actor judges that a human should look at
the change and runs `lode review add <task>`. Any human may do that on any task
at any time.

The worker that built the change is the first actor that decides without being
asked. Before `lode task submit`, it opens a review when the change does any of
these:

- adds a migration, or changes how stored data is written or read;
- touches authentication, authorization, tokens, secrets, or the route guard
  table;
- changes deploy manifests, CI workflows, or release configuration;
- removes or renames a CLI verb, an API route, or a wire field;
- departs from a governing rule, or settles a design question its plan left
  open;
- leaves a doubt the worker's own review could not resolve, such as behavior
  the tests cannot observe.

Otherwise the worker opens no review, and the change merges on CI and the
worker's own review. The list is a floor: the worker may open a review for a
reason not on it, and says which.

